// Package qase uploads steve-tests case definitions to Qase and reports Go
// test results into Qase test runs. It mirrors the reporting used by
// rancher/wrangler-tests so both suites land in Qase the same way.
package qase

const (
	// AutomationTestNameID is the Qase custom field that can hold a Go test
	// name when it differs from the case title.
	AutomationTestNameID = int64(15)
	// RancherManagerProjectID is the Qase project used when none is configured.
	RancherManagerProjectID = "RM"

	QaseTokenEnvVar       = "QASE_AUTOMATION_TOKEN"
	APIURLEnvVar          = "QASE_API_URL"
	TestRunEnvVar         = "QASE_TEST_RUN_ID"
	ProjectIDEnvVar       = "QASE_PROJECT_ID"
	TestRunNameEnvVar     = "TEST_RUN_NAME"
	TestRunCompleteEnvVar = "QASE_TEST_RUN_COMPLETE"
	SchemaPrefixEnvVar    = "QASE_SCHEMA_PREFIX"
	CustomFieldFilterEnv  = "QASE_CUSTOM_FIELD_FILTER"
	TestResultsEnvVar     = "QASE_TEST_RESULTS"

	TestResultsJSON = "results.json"
	PassStatus      = "pass"
	FailStatus      = "fail"
	SkipStatus      = "skip"

	// recurringRunSourceField and recurringRunValue tag runs created in the
	// RM project as recurring automation runs.
	recurringRunSourceField = 16
	recurringRunValue       = 1
	requestLimit            = 100
	schemaFileSuffix        = "schemas.yaml"
)
