// Command reporter-v2 posts go test -json results to a Qase test run.
//
// Each top-level Go test listed in the schemas directory maps to one Qase case.
// Tests that are not in a schema, such as harness unit tests, are not reported.
//
// Environment:
//
//	QASE_AUTOMATION_TOKEN   Qase API token.
//	QASE_PROJECT_ID         Qase project code (default RM).
//	QASE_TEST_RUN_ID        Existing run to report into, or
//	TEST_RUN_NAME           name of a new run to create.
//	QASE_TEST_RUN_COMPLETE  true to complete the run after reporting.
//	QASE_SCHEMA_PREFIX      Only read schema files named <prefix>_*schemas.yaml.
//	QASE_CUSTOM_FIELD_FILTER  Only match cases carrying this custom field value.
//	QASE_TEST_RESULTS       go test -json file (default results.json).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/rancher/steve-tests/extensions/qase"
	"github.com/rancher/steve-tests/extensions/qase/testresult"
)

const (
	// Qase limits run descriptions to 10000 characters.
	descriptionLimit = 10000
	// commentOutputLimit keeps result comments readable; the end of the
	// output, where failures are reported, is kept.
	commentOutputLimit = 20000
)

var qaseStatus = map[string]string{
	qase.PassStatus: "passed",
	qase.FailStatus: "failed",
	qase.SkipStatus: "skipped",
}

func main() {
	resultsPath := flag.String("results", envOr(qase.TestResultsEnvVar, qase.TestResultsJSON), "go test -json results file")
	schemaDir := flag.String("schemas", "schemas", "directory holding *schemas.yaml files")
	flag.Parse()

	if err := run(context.Background(), *resultsPath, *schemaDir); err != nil {
		slog.Error("Qase reporting failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, resultsPath, schemaDir string) error {
	slog.Info("running Qase reporter v2")

	project := os.Getenv(qase.ProjectIDEnvVar)
	if project == "" {
		slog.Warn("Qase project not provided; using the default", "project", qase.RancherManagerProjectID)
		project = qase.RancherManagerProjectID
	}

	runIDValue := os.Getenv(qase.TestRunEnvVar)
	runName := os.Getenv(qase.TestRunNameEnvVar)
	if runIDValue == "" && runName == "" {
		slog.Warn("no Qase run ID or run name provided; nothing to report")
		return nil
	}

	results, err := readResults(resultsPath)
	if err != nil {
		return err
	}
	schemas, err := qase.GetSchemasByPrefix(schemaDir, os.Getenv(qase.SchemaPrefixEnvVar))
	if err != nil {
		return fmt.Errorf("read schemas from %s: %w", schemaDir, err)
	}
	if len(schemas) == 0 {
		return fmt.Errorf("no schema files found in %s", schemaDir)
	}
	mapped, err := mapSchemaResults(project, results, schemas)
	if err != nil {
		return err
	}

	service := qase.SetupQaseClient()

	var runID int64
	if runName != "" {
		runID, err = service.CreateTestRun(ctx, runName, project, RunDescription())
		if err != nil {
			return err
		}
		slog.Info("created Qase run", "id", runID, "name", runName)
	} else {
		runID, err = strconv.ParseInt(runIDValue, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid %s %q: %w", qase.TestRunEnvVar, runIDValue, err)
		}
	}

	reportErr := report(ctx, service, project, int32(runID), mapped, schemas)

	if complete, _ := strconv.ParseBool(os.Getenv(qase.TestRunCompleteEnvVar)); complete && reportErr == nil {
		if err := service.CompleteTestRun(ctx, project, int32(runID)); err != nil {
			return err
		}
		slog.Info("completed Qase run", "id", runID)
	}
	return reportErr
}

func readResults(path string) ([]*testresult.GoTestResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open test results: %w", err)
	}
	defer file.Close()

	results, err := testresult.Parse(file)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	slog.Info("read Go test results", "file", path, "tests", len(results))
	return results, nil
}

type schemaResult struct {
	result     *testresult.GoTestResult
	caseSchema *upstream.TestCaseCreate
	suitePath  string
}

func mapSchemaResults(project string, results []*testresult.GoTestResult, schemas []qase.TestSuiteSchema) ([]schemaResult, error) {
	var mapped []schemaResult
	for _, result := range results {
		schemaCase, suite, err := qase.GetTestSchemaWithSuite(result.Name, schemas)
		if errors.Is(err, qase.ErrTestSchemaNotFound) {
			slog.Debug("test is not in a Qase schema; not reporting it", "test", result.Name, "package", result.Package)
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(suite.Projects) > 0 && !slices.Contains(suite.Projects, project) {
			return nil, fmt.Errorf("schema suite %q targets projects %v, not configured Qase project %s", suite.Suite, suite.Projects, project)
		}
		mapped = append(mapped, schemaResult{result: result, caseSchema: schemaCase, suitePath: suite.Suite})
	}
	if len(mapped) == 0 {
		return nil, fmt.Errorf("no test results match the Qase schemas; check the test selection and schema prefix")
	}
	return mapped, nil
}

func report(ctx context.Context, service *qase.Service, project string, runID int32, results []schemaResult, schemas []qase.TestSuiteSchema) error {
	var (
		errs     []error
		reported int
		seen     = map[string]bool{}
		suiteIDs = map[string]int64{}
	)

	for _, mapped := range results {
		result, schemaCase := mapped.result, mapped.caseSchema
		seen[mapped.suitePath+"/"+schemaCase.Title] = true
		suiteID, ok := suiteIDs[mapped.suitePath]
		if !ok {
			var err error
			suiteID, err = service.GetTestSuitePath(ctx, project, mapped.suitePath)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", result.Name, err))
				continue
			}
			suiteIDs[mapped.suitePath] = suiteID
		}

		cases, err := service.FindTestCases(ctx, project, result.Name, os.Getenv(qase.CustomFieldFilterEnv), suiteID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		switch len(cases) {
		case 0:
			errs = append(errs, fmt.Errorf("%s: no Qase case found in project %s suite %q; upload the schemas first", result.Name, project, mapped.suitePath))
			continue
		case 1:
		default:
			errs = append(errs, fmt.Errorf("%s: %d Qase cases match in suite %q; resolve duplicates before reporting", result.Name, len(cases), mapped.suitePath))
			continue
		}

		body := resultBody(result, *cases[0].Id, schemaCase.Parameters)
		if _, _, err := service.Client.ResultsAPI.CreateResult(ctx, project, runID).ResultCreate(body).Execute(); err != nil {
			errs = append(errs, fmt.Errorf("%s: post result: %w", result.Name, err))
			continue
		}
		reported++
		slog.Info("reported test", "test", result.Name, "status", body.Status)
	}

	var missing []string
	for _, suite := range schemas {
		for _, c := range suite.Cases {
			if !seen[suite.Suite+"/"+c.Title] {
				missing = append(missing, c.Title)
			}
		}
	}
	if len(missing) > 0 {
		slog.Warn("schema cases with no result in this run", "cases", strings.Join(missing, ", "))
	}

	slog.Info("Qase reporting finished", "reported", reported, "errors", len(errs))
	if len(errs) > 0 {
		return fmt.Errorf("failed to report %d test(s): %w", len(errs), errors.Join(errs...))
	}
	if reported == 0 {
		return fmt.Errorf("no test results were reported to Qase")
	}
	return nil
}

func resultBody(result *testresult.GoTestResult, caseID int64, params []upstream.TestCaseParameterCreate) upstream.ResultCreate {
	status, ok := qaseStatus[result.Status]
	if !ok {
		status = "failed"
	}

	elapsedMs := int64(result.Elapsed * 1000)
	comment := Comment(result)
	body := upstream.ResultCreate{
		CaseId:  &caseID,
		Status:  status,
		TimeMs:  *upstream.NewNullableInt64(&elapsedMs),
		Comment: *upstream.NewNullableString(&comment),
	}
	if status == "failed" {
		stack := truncateHead(result.Output, commentOutputLimit)
		body.Stacktrace = *upstream.NewNullableString(&stack)
	}

	resultParams := map[string]string{}
	for _, param := range params {
		if param.ParameterSingle == nil {
			continue
		}
		if value := strings.Join(param.ParameterSingle.Values, ", "); value != "" {
			resultParams[param.ParameterSingle.Title] = value
		}
	}
	if len(resultParams) > 0 {
		body.Param = resultParams
	}
	return body
}

// Comment summarizes a test result for Qase: where it ran, how its subtests
// fared, and the output of anything that did not pass.
func Comment(result *testresult.GoTestResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Go test: %s\nPackage: %s\n", result.Name, result.Package)

	status := qaseStatus[result.Status]
	if status == "" {
		status = "failed"
	}
	fmt.Fprintf(&b, "Result: %s in %s\n", status, time.Duration(result.Elapsed*float64(time.Second)).Round(time.Millisecond))
	if result.Interrupted {
		b.WriteString("The test never reported a result. The package most likely panicked or hit its timeout.\n")
	}

	if len(result.Subtests) > 0 {
		passed, failed, skipped := result.Counts()
		fmt.Fprintf(&b, "Subtests: %d passed, %d failed, %d skipped\n", passed, failed, skipped)
		var failing []string
		for _, sub := range result.Subtests {
			if sub.Status == qase.FailStatus {
				failing = append(failing, "- "+sub.Name)
			}
		}
		if len(failing) > 0 {
			b.WriteString("Failed subtests:\n" + strings.Join(failing, "\n") + "\n")
		}
	}

	if out := strings.TrimSpace(result.Output); out != "" && result.Status != qase.PassStatus {
		b.WriteString("\nOutput:\n")
		b.WriteString(truncateHead(out, commentOutputLimit))
		b.WriteString("\n")
	}
	return b.String()
}

// truncateHead keeps the last limit bytes of s.
func truncateHead(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := len(s) - limit
	if i := strings.IndexByte(s[cut:], '\n'); i >= 0 {
		cut += i + 1
	}
	return "[earlier output truncated]\n" + s[cut:]
}

// RunDescription describes where a run came from, using the GitHub Actions
// and e2e environment when present.
func RunDescription() string {
	var lines []string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, label+": "+value)
		}
	}

	if server, repo, id := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_RUN_ID"); server != "" && repo != "" && id != "" {
		add("GitHub Actions run", fmt.Sprintf("%s/%s/actions/runs/%s", server, repo, id))
	}
	add("steve-tests commit", os.Getenv("GITHUB_SHA"))
	add("Steve version", os.Getenv("STEVE_VERSION"))
	add("Steve commit", os.Getenv("STEVE_COMMIT"))
	add("Kubernetes version", os.Getenv("KUBE_VERSION"))

	description := strings.Join(lines, "\n")
	if len(description) > descriptionLimit {
		description = description[:descriptionLimit]
	}
	return description
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
