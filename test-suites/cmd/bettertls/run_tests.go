package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Netflix/bettertls/test-suites/impltests"
	int_set "github.com/Netflix/bettertls/test-suites/int-set"
	test_executor "github.com/Netflix/bettertls/test-suites/test-executor"
	"github.com/golang/protobuf/proto"
	"github.com/schollz/progressbar/v3"
	"os"
	"path/filepath"
	"time"
	"sort"
)

type ImplementationTestResults struct {
	ImplementationInfo string            `json:"implementation"`
	VersionInfo        string            `json:"version"`
	Date               time.Time         `json:"date"`
	BetterTlsRevision  string            `json:"betterTlsRevision"`
	Suites             map[string][]byte `json:"suites"`
}

func runTests(args []string) error {
	flagSet := flag.NewFlagSet("run-tests", flag.ContinueOnError)
	var implementation string
	flagSet.StringVar(&implementation, "implementation", "", "Implementation to test.")
	var suite string
	flagSet.StringVar(&suite, "suite", "", "Run only the given suite instead of all suites.")
	testCases := new(int_set.IntSet)
	flagSet.Var(testCases, "testCase", "Run only the given test case(s) in the suite instead of all tests. Requires --suite to be specified as well. Use \"123,456-789\" syntax to include a range or set of cases.")
	var outputDir string
	flagSet.StringVar(&outputDir, "outputDir", ".", "Directory to which test results will be written.")

	err := flagSet.Parse(args)
	if err != nil {
		return err
	}

	manifest, err := buildManifest()
	if err != nil {
		return err
	}

	var runners []impltests.ImplementationRunner
	if implementation == "" {
		for _, runner := range impltests.Runners {
			runners = append(runners, runner)
		}
	} else {
		runner := impltests.Runners[implementation]
		if runner == nil {
			return fmt.Errorf("invalid implementation: %s", implementation)
		}
		runners = []impltests.ImplementationRunner{runner}
	}

	// Track overall test run timing
	overallStartTime := time.Now()
	var runErrors []error

	for _, runner := range runners {
		// Track total runner execution time
		runnerStartTime := time.Now()
	
		err := runner.Initialize()
		if err != nil {
			runErrors = append(runErrors, fmt.Errorf("failed to initialize runner %s: %v", runner.Name(), err))
			continue
		}
	
		var bar *progressbar.ProgressBar
		
		// Prepare a map to store test timing information
		testTimings := make(map[string]*struct {
			StartTime time.Time
			Duration  time.Duration
		})
		
		ctx := &test_executor.ExecutionContext{
			RunOnlySuite: suite,
			RunOnlyTests: testCases,
			OnStartSuite: func(suite string, testCount uint) {
				bar = progressbar.Default(int64(testCount), runner.Name()+"/"+suite)
				progressbar.OptionSetItsString("tests")(bar)
			},
			OnStartTest: func(idx uint) {
				bar.Add(1)
			},
			OnTestStart: func(idx uint) {
				testKey := fmt.Sprintf("%s/test-%d", suite, idx)
        		// fmt.Printf("Debug: Starting test %s\n", testKey)
				testTimings[testKey] = &struct {
					StartTime time.Time
					Duration  time.Duration
				}{
					StartTime: time.Now(),
					Duration:  0,
				}
			},
			OnTestEnd: func(idx uint) {
				testKey := fmt.Sprintf("%s/test-%d", suite, idx)
				if timing := testTimings[testKey]; timing != nil {
					timing.Duration = time.Since(timing.StartTime)
				}
			},
		}
	
		version := runner.GetVersion()
		suiteResults, err := runner.RunTests(ctx)
		if err != nil {
			runErrors = append(runErrors, fmt.Errorf("error running tests for %s: %v", runner.Name(), err))
			continue
		}
	
		// Calculate total runner execution time
		runnerDuration := time.Since(runnerStartTime)
	
		// Print timing information
		fmt.Printf("Timing Report for Runner: %s\n", runner.Name())
		fmt.Printf("Total Runner Execution Time: %v\n", runnerDuration)
		
		// Print individual test timings (sorted for readability)
		var testKeys []string
		for name := range testTimings {
			testKeys = append(testKeys, name)
		}
		sort.Strings(testKeys)
		
		// Print individual test timings
		fmt.Println("Individual Test Timings:")
		var totalTestTime time.Duration
		if len(testKeys) == 0 {
			fmt.Println("  No test timing data available")
		} else {
			for _, name := range testKeys {
				if timing := testTimings[name]; timing != nil {
					// Only print if time is non-zero
					if timing.Duration == 0 {
						continue
					}
					fmt.Printf("  %s: %v\n", name, timing.Duration)
					totalTestTime += timing.Duration
				}
			}
		}
		fmt.Printf("Total Test Execution Time: %v\n", totalTestTime)
		fmt.Println()
	
		suiteResultsEncoded := make(map[string][]byte, len(suiteResults))
		for suiteName, result := range suiteResults {
			resultBytes, err := proto.Marshal(result)
			if err != nil {
				runErrors = append(runErrors, fmt.Errorf("failed to proto-marshal results for %s: %v", suiteName, err))
				continue
			}
			buffer := bytes.NewBuffer(nil)
			gz := gzip.NewWriter(buffer)
			_, err = gz.Write(resultBytes)
			if err != nil {
				runErrors = append(runErrors, fmt.Errorf("failed to gzip results for %s: %v", suiteName, err))
				continue
			}
			if err = gz.Flush(); err != nil {
				runErrors = append(runErrors, fmt.Errorf("failed to flush gzip for %s: %v", suiteName, err))
				continue
			}
			if err = gz.Close(); err != nil {
				runErrors = append(runErrors, fmt.Errorf("failed to close gzip for %s: %v", suiteName, err))
				continue
			}
			suiteResultsEncoded[suiteName] = buffer.Bytes()
		}
	
		results := &ImplementationTestResults{
			ImplementationInfo: runner.Name(),
			VersionInfo:        version,
			Date:               time.Now(),
			BetterTlsRevision:  test_executor.GetBuildRevision(),
			Suites:             suiteResultsEncoded,
		}
	
		f, err := os.OpenFile(filepath.Join(outputDir, fmt.Sprintf("%s_results.json", runner.Name())),
			os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			runErrors = append(runErrors, fmt.Errorf("failed to open file for saving results for %s: %v", runner.Name(), err))
			continue
		}
		err = json.NewEncoder(f).Encode(results)
		f.Close()
		if err != nil {
			runErrors = append(runErrors, fmt.Errorf("failed to save results for %s: %v", runner.Name(), err))
			continue
		}
	
		if summary, err := buildSummary(results, manifest); err == nil {
			printSummary(summary)
		}
	}

	// Overall timing and error reporting
	overallDuration := time.Since(overallStartTime)
	fmt.Printf("Total Test Run Time: %v\n", overallDuration)

	// Aggregate and report any errors
	if len(runErrors) > 0 {
		fmt.Println("\nEncountered Errors:")
		for _, e := range runErrors {
			fmt.Println(e)
		}
		return fmt.Errorf("encountered %d errors during test run", len(runErrors))
	}

	return nil
}