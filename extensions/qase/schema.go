package qase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"gopkg.in/yaml.v3"
)

// TestSuiteSchema describes one Qase suite and its cases. Case fields use the
// Qase API's JSON names, for example is_flaky and custom_field.
type TestSuiteSchema struct {
	Projects []string                  `json:"projects,omitempty"`
	Suite    string                    `json:"suite,omitempty"`
	Cases    []upstream.TestCaseCreate `json:"cases,omitempty"`
}

// ReadSchemaFile parses one schema file.
func ReadSchemaFile(path string) ([]TestSuiteSchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSchema(data)
}

// ParseSchema decodes YAML through JSON so the Qase client's JSON field names
// apply. Decoding YAML straight into the client structs would silently drop
// snake_case keys such as is_flaky.
func ParseSchema(data []byte) ([]TestSuiteSchema, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse schema YAML: %w", err)
	}
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("convert schema to JSON: %w", err)
	}

	var suites []TestSuiteSchema
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suites); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	return suites, nil
}

func isSchemaFile(name, prefix string) bool {
	if !strings.HasSuffix(name, schemaFileSuffix) {
		return false
	}
	prefix = strings.TrimSpace(prefix)
	return prefix == "" || strings.HasPrefix(name, prefix+"_")
}

// GetSchemasByPrefix reads every *schemas.yaml file under basePath. A non-empty
// prefix limits it to files named <prefix>_*schemas.yaml.
func GetSchemasByPrefix(basePath, prefix string) ([]TestSuiteSchema, error) {
	var all []TestSuiteSchema
	err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isSchemaFile(d.Name(), prefix) {
			return nil
		}
		suites, err := ReadSchemaFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		all = append(all, suites...)
		return nil
	})
	return all, err
}

// GetTestSchema finds the case for a Go test by title or by the automation
// test name custom field.
func GetTestSchema(testName string, suites []TestSuiteSchema) (*upstream.TestCaseCreate, error) {
	testCase, _, err := GetTestSchemaWithSuite(testName, suites)
	return testCase, err
}

// ErrTestSchemaNotFound identifies tests outside the configured schemas.
var ErrTestSchemaNotFound = errors.New("test case not found in schemas")

// GetTestSchemaWithSuite also retains the suite path used to identify the Qase
// case. Ambiguous schema mappings fail instead of picking an arbitrary suite.
func GetTestSchemaWithSuite(testName string, suites []TestSuiteSchema) (*upstream.TestCaseCreate, *TestSuiteSchema, error) {
	var match *upstream.TestCaseCreate
	var matchedSuite *TestSuiteSchema
	for _, suite := range suites {
		for i := range suite.Cases {
			testCase := suite.Cases[i]
			matches := testCase.Title == testName
			if testCase.CustomField != nil {
				if name, ok := (*testCase.CustomField)[strconv.FormatInt(AutomationTestNameID, 10)]; ok && name == testName {
					matches = true
				}
			}
			if !matches {
				continue
			}
			if match != nil {
				return nil, nil, fmt.Errorf("multiple schema cases match test %s", testName)
			}
			match, matchedSuite = &testCase, &suite
		}
	}
	if match == nil {
		return nil, nil, fmt.Errorf("%s: %w", testName, ErrTestSchemaNotFound)
	}
	return match, matchedSuite, nil
}

// UploadSchemas creates or updates every suite and case found under basePath.
func UploadSchemas(ctx context.Context, client *Service, basePath, prefix string) error {
	suites, err := GetSchemasByPrefix(basePath, prefix)
	if err != nil {
		return fmt.Errorf("read test schemas: %w", err)
	}
	if len(suites) == 0 {
		return fmt.Errorf("no schema files found under %s", basePath)
	}

	var errs []error
	for _, suite := range suites {
		for _, project := range suite.Projects {
			slog.Info("uploading suite", "suite", suite.Suite, "project", project)
			suiteID, err := client.createSuitePath(ctx, project, suite.Suite)
			if err != nil {
				errs = append(errs, fmt.Errorf("suite %s in %s: %w", suite.Suite, project, err))
				continue
			}

			var cases []upstream.TestCaseCreate
			for _, tc := range suite.Cases {
				if tc.Title == "" {
					continue
				}
				id := suiteID
				tc.SuiteId = &id
				cases = append(cases, tc)
			}
			if err := client.UploadTests(ctx, project, cases); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
