package qase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
)

// Service wraps the Qase API client.
type Service struct {
	Client *upstream.APIClient
}

// SetupQaseClient creates a Qase client using the token in
// QASE_AUTOMATION_TOKEN. QASE_API_URL overrides the API endpoint.
func SetupQaseClient() *Service {
	cfg := upstream.NewConfiguration()
	cfg.AddDefaultHeader("Token", os.Getenv(QaseTokenEnvVar))
	if apiURL := strings.TrimSpace(os.Getenv(APIURLEnvVar)); apiURL != "" {
		cfg.Servers = upstream.ServerConfigurations{{URL: strings.TrimRight(apiURL, "/")}}
	}
	return &Service{Client: upstream.NewAPIClient(cfg)}
}

// GetTestSuite returns the suite with the given title and parent, if it exists.
func (q *Service) GetTestSuite(ctx context.Context, project, suite string, parentID *int64) (*upstream.Suite, error) {
	slog.Debug("getting test suite", "suite", suite, "project", project)

	var offset int32
	var match *upstream.Suite
	for {
		suites, _, err := q.Client.SuitesAPI.GetSuites(ctx, project).
			Search(suite).Limit(requestLimit).Offset(offset).Execute()
		if err != nil {
			return nil, fmt.Errorf("list suites matching %q: %w", suite, err)
		}

		entities := suites.Result.Entities
		for i := range entities {
			result := entities[i]
			if result.Title == nil || *result.Title != suite {
				continue
			}
			if sameParent(parentID, result.ParentId.Get()) {
				if match != nil {
					return nil, fmt.Errorf("multiple Qase suites named %q have the same parent in project %s", suite, project)
				}
				match = &result
			}
		}

		if len(entities) < requestLimit {
			return match, nil
		}
		offset += int32(len(entities))
	}
}

func sameParent(want, got *int64) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}
	return *want == *got
}

// CreateTestSuite creates a suite and returns its ID.
func (q *Service) CreateTestSuite(ctx context.Context, project string, suite upstream.SuiteCreate) (int64, error) {
	slog.Debug("creating test suite", "suite", suite.Title, "project", project)
	id, _, err := q.Client.SuitesAPI.CreateSuite(ctx, project).SuiteCreate(suite).Execute()
	if err != nil {
		return 0, fmt.Errorf("create test suite %q: %w", suite.Title, err)
	}
	return *id.Result.Id, nil
}

// createSuitePath creates any missing suites in a slash-separated path and
// returns the ID of the last one.
func (q *Service) createSuitePath(ctx context.Context, project, suitePath string) (int64, error) {
	return q.resolveSuitePath(ctx, project, suitePath, true)
}

// GetTestSuitePath resolves an existing slash-separated suite path. Reporting
// must not create suites, because the matching cases must already be uploaded.
func (q *Service) GetTestSuitePath(ctx context.Context, project, suitePath string) (int64, error) {
	return q.resolveSuitePath(ctx, project, suitePath, false)
}

func (q *Service) resolveSuitePath(ctx context.Context, project, suitePath string, create bool) (int64, error) {
	titles := strings.Split(suitePath, "/")
	for _, title := range titles {
		if strings.TrimSpace(title) == "" {
			return 0, fmt.Errorf("invalid empty component in suite path %q", suitePath)
		}
	}
	var parentID *int64
	for _, title := range titles {
		existing, err := q.GetTestSuite(ctx, project, title, parentID)
		if err != nil {
			return 0, err
		}

		var id int64
		if existing != nil {
			if existing.Id == nil || *existing.Id <= 0 {
				return 0, fmt.Errorf("Qase suite %q has no valid ID", suitePath)
			}
			id = *existing.Id
		} else {
			if !create {
				return 0, fmt.Errorf("Qase suite %q not found in project %s; upload the schemas first", suitePath, project)
			}
			body := upstream.SuiteCreate{Title: title}
			if parentID != nil {
				body.ParentId = *upstream.NewNullableInt64(parentID)
			}
			id, err = q.CreateTestSuite(ctx, project, body)
			if err != nil {
				return 0, err
			}
		}
		parentID = &id
	}

	if parentID == nil {
		return 0, fmt.Errorf("empty suite path")
	}
	return *parentID, nil
}

// UploadTests creates each case, or updates it when exactly one case with the
// same title or automation name already exists in its target suite.
func (q *Service) UploadTests(ctx context.Context, project string, testCases []upstream.TestCaseCreate) error {
	var errs []error
	for _, tc := range testCases {
		if tc.SuiteId == nil || *tc.SuiteId <= 0 {
			errs = append(errs, fmt.Errorf("test case %q has no target suite ID", tc.Title))
			continue
		}
		matches, err := q.FindTestCases(ctx, project, tc.Title, "", *tc.SuiteId)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		switch len(matches) {
		case 0:
			slog.Info("creating test case", "project", project, "title", tc.Title, "suiteID", *tc.SuiteId)
			if _, _, err := q.Client.CasesAPI.CreateCase(ctx, project).TestCaseCreate(tc).Execute(); err != nil {
				errs = append(errs, fmt.Errorf("create test case %q: %w", tc.Title, err))
			}
		case 1:
			slog.Info("updating test case", "project", project, "title", tc.Title, "suiteID", *tc.SuiteId)
			update, err := toCaseUpdate(tc)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if _, _, err := q.Client.CasesAPI.UpdateCase(ctx, project, int32(*matches[0].Id)).TestCaseUpdate(update).Execute(); err != nil {
				errs = append(errs, fmt.Errorf("update test case %q: %w", tc.Title, err))
			}
		default:
			errs = append(errs, fmt.Errorf("test case %q: %d Qase cases match in suite %d; resolve duplicates before uploading", tc.Title, len(matches), *tc.SuiteId))
		}
	}
	return errors.Join(errs...)
}

func toCaseUpdate(tc upstream.TestCaseCreate) (upstream.TestCaseUpdate, error) {
	var update upstream.TestCaseUpdate
	data, err := json.Marshal(tc)
	if err != nil {
		return update, fmt.Errorf("encode test case %q: %w", tc.Title, err)
	}
	if err := json.Unmarshal(data, &update); err != nil {
		return update, fmt.Errorf("convert test case %q: %w", tc.Title, err)
	}
	return update, nil
}

// FindTestCases returns cases whose title exactly matches title, or whose
// automation test name custom field does, within the specified suite only.
// When customFieldValue is set, only cases carrying that value are returned.
func (q *Service) FindTestCases(ctx context.Context, project, title, customFieldValue string, suiteID int64) ([]upstream.TestCase, error) {
	var (
		matches []upstream.TestCase
		offset  int32
	)
	for {
		resp, _, err := q.Client.CasesAPI.GetCases(ctx, project).
			Search(title).Limit(requestLimit).Offset(offset).Execute()
		if err != nil {
			return nil, fmt.Errorf("search Qase cases for %q: %w", title, err)
		}

		entities := resp.Result.Entities
		for _, tc := range entities {
			if tc.SuiteId.Get() == nil || *tc.SuiteId.Get() != suiteID {
				continue
			}
			if !caseMatchesTitle(tc, title) {
				continue
			}
			if customFieldValue != "" && !hasCustomFieldValue(tc.CustomFields, customFieldValue) {
				continue
			}
			matches = append(matches, tc)
		}

		if len(entities) < requestLimit {
			return matches, nil
		}
		offset += int32(len(entities))
	}
}

func caseMatchesTitle(tc upstream.TestCase, title string) bool {
	if tc.Title != nil && *tc.Title == title {
		return true
	}
	for _, field := range tc.CustomFields {
		if field.Id != nil && *field.Id == AutomationTestNameID && field.Value != nil && *field.Value == title {
			return true
		}
	}
	return false
}

func hasCustomFieldValue(fields []upstream.CustomFieldValue, value string) bool {
	for _, field := range fields {
		if field.Value != nil && *field.Value == value {
			return true
		}
	}
	return false
}

// CreateTestRun creates a run and returns its ID.
func (q *Service) CreateTestRun(ctx context.Context, name, project, description string) (int64, error) {
	body := upstream.RunCreate{Title: name}
	if project == RancherManagerProjectID {
		body.CustomField = &map[string]string{
			strconv.Itoa(recurringRunSourceField): strconv.Itoa(recurringRunValue),
		}
	}
	if description != "" {
		body.SetDescription(description)
	}

	resp, _, err := q.Client.RunsAPI.CreateRun(ctx, project).RunCreate(body).Execute()
	if err != nil {
		return 0, fmt.Errorf("create test run %q: %w", name, err)
	}
	return *resp.Result.Id, nil
}

// CompleteTestRun marks a run complete.
func (q *Service) CompleteTestRun(ctx context.Context, project string, runID int32) error {
	if _, _, err := q.Client.RunsAPI.CompleteRun(ctx, project, runID).Execute(); err != nil {
		return fmt.Errorf("complete test run %d: %w", runID, err)
	}
	return nil
}
