package test_executor

import (
	"fmt"
	int_set "github.com/Netflix/bettertls/test-suites/int-set"
	test_case "github.com/Netflix/bettertls/test-suites/test-case"
)

type ExecutionContext struct {
    OnStartSuite   func(suite string, testCount uint)
    OnStartTest    func(idx uint)
    OnFinishTest   func(idx uint)
    OnFinishSuite  func(suite string)
    RunOnlySuite   string
    RunOnlyTests   *int_set.IntSet
    OnTestStart    func(idx uint)
    OnTestEnd      func(idx uint)
}

func ExecuteAllTestsLocal(ctx *ExecutionContext, suites *TestSuites, execTest func(hostname string, certificates [][]byte) (bool, error)) (map[string]*SuiteTestResults, error) {
	return executeAllTests(ctx, suites, func(index uint, provider test_case.TestCaseProvider, testCase test_case.TestCase) (bool, error) {
		certs, err := testCase.GetCertificates(suites.rootCert, suites.rootKey)
		if err != nil {
			return false, err
		}
		return execTest(testCase.GetHostname(), certs.Certificate)
	})
}

func ExecuteAllTestsRemote(ctx *ExecutionContext, suites *TestSuites, execTest func(hostname string, port uint) (bool, error)) (map[string]*SuiteTestResults, error) {
	server, err := StartServer(suites, noplog, 0, 0)
	if err != nil {
		return nil, err
	}
	defer server.Stop()

	return executeAllTests(ctx, suites, func(index uint, provider test_case.TestCaseProvider, testCase test_case.TestCase) (bool, error) {
		server.SetTest(provider.Name(), index)
		return execTest(testCase.GetHostname(), uint(server.tlsPort))
	})
}

func executeAllTests(ctx *ExecutionContext, suites *TestSuites, execTest func(index uint, provider test_case.TestCaseProvider, testCase test_case.TestCase) (bool, error)) (map[string]*SuiteTestResults, error) {
	results := make(map[string]*SuiteTestResults)
	for _, name := range suites.GetProviderNames() {
		if ctx != nil && ctx.RunOnlySuite != "" && ctx.RunOnlySuite != name {
			continue
		}
		provider := suites.GetProvider(name)
		suiteResults, err := executeTestsForProvider(ctx, provider, func(index uint, testCase test_case.TestCase) (bool, error) {
			return execTest(index, provider, testCase)
		})
		if err != nil {
			return nil, err
		}
		results[name] = suiteResults
	}
	return results, nil
}

func executeTestsForProvider(ctx *ExecutionContext, provider test_case.TestCaseProvider, execTest func(index uint, testCase test_case.TestCase) (bool, error)) (*SuiteTestResults, error) {
	execTestCase := func(idx uint, testCase test_case.TestCase) (TestCaseResult, error) {
		result, err := execTest(idx, testCase)
		if err != nil {
			return TestCaseResult_ACCEPTED, fmt.Errorf("test case %d failed: %v", idx, err)
		}
		if result {
			return TestCaseResult_ACCEPTED, nil
		}
		return TestCaseResult_REJECTED, nil
	}

	matchesExpected := func(r TestCaseResult, expected test_case.ExpectedResult) bool {
		if expected == test_case.EXPECTED_RESULT_PASS && r != TestCaseResult_ACCEPTED {
			return false
		}
		if expected == test_case.EXPECTED_RESULT_FAIL && r != TestCaseResult_REJECTED {
			return false
		}
		return true
	}

	testCaseCount, err := provider.GetTestCaseCount()
	if err != nil {
		return nil, err
	}

	if ctx != nil && ctx.OnStartSuite != nil {
		ctx.OnStartSuite(provider.Name(), testCaseCount)
	}

	sanityCheckTestCaseId, err := provider.GetSanityCheckTestCase()
	if err != nil {
		return nil, fmt.Errorf("failed to get sanity check test case ID: %v", err)
	}

	sanityCheckTestCase, err := provider.GetTestCase(sanityCheckTestCaseId)
	if err != nil {
		return nil, fmt.Errorf("failed to get sanity check test case for ID %d: %v", 
			sanityCheckTestCaseId, err)
	}

	sanityCheckResult, err := execTestCase(sanityCheckTestCaseId, sanityCheckTestCase)
	if err != nil {
		return nil, fmt.Errorf("failed to execute sanity check test case: %v", err)
	}

	if !matchesExpected(sanityCheckResult, sanityCheckTestCase.ExpectedResult()) {
		return nil, fmt.Errorf("sanity check failed: got result %v but expected %v for test case %d", 
			sanityCheckResult, sanityCheckTestCase.ExpectedResult(), sanityCheckTestCaseId)
	}

	allFeatures := provider.GetFeatures()
	supportedFeatures := make(map[test_case.Feature]bool)
	for _, feature := range allFeatures {
		testCases, err := provider.GetTestCasesForFeature(feature)
		if err != nil {
			return nil, err
		}
		hasFailure := false
		for _, idx := range testCases {
			tc, err := provider.GetTestCase(idx)
			if err != nil {
				return nil, err
			}
			res, err := execTestCase(idx, tc)
			if err != nil {
				return nil, err
			}
			if !matchesExpected(res, tc.ExpectedResult()) {
				hasFailure = true
				break
			}
		}
		supportedFeatures[feature] = !hasFailure
	}

	results := make([]TestCaseResult, testCaseCount)
	for idx := uint(0); idx < testCaseCount; idx += 1 {
		if ctx != nil && ctx.RunOnlyTests != nil && !ctx.RunOnlyTests.Empty() && !ctx.RunOnlyTests.Contains(int(idx)) {
			results[idx] = TestCaseResult_SKIPPED
			continue
		}
		if ctx != nil && ctx.OnStartTest != nil {
			ctx.OnStartTest(idx)
		}
		if ctx != nil && ctx.OnTestStart != nil {
			ctx.OnTestStart(idx)
		}
		testCase, err := provider.GetTestCase(idx)
		if err != nil {
			return nil, err
		}

		allFeaturesSupported := true
		for _, feature := range testCase.RequiredFeatures() {
			if !supportedFeatures[feature] {
				allFeaturesSupported = false
				break
			}
		}
		if !allFeaturesSupported {
			results[idx] = TestCaseResult_SKIPPED
			continue
		}

		testResult, err := execTestCase(idx, testCase)
		if err != nil {
			return nil, err
		}
		results[idx] = testResult

		if ctx != nil && ctx.OnTestEnd != nil {
			ctx.OnTestEnd(idx)
		}

		if ctx != nil && ctx.OnFinishTest != nil {
			ctx.OnFinishTest(idx)
		}
	}

	if ctx != nil && ctx.OnFinishSuite != nil {
		ctx.OnFinishSuite(provider.Name())
	}

	output := &SuiteTestResults{
		TestCaseResults: results,
	}
	for _, feature := range provider.GetFeatures() {
		if supportedFeatures[feature] {
			output.SupportedFeatures = append(output.SupportedFeatures, int32(feature))
		} else {
			output.UnsupportedFeatures = append(output.UnsupportedFeatures, int32(feature))
		}
	}
	return output, nil
}
