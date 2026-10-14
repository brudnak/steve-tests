// Package query builds Steve list queries without hand-assembling URL strings.
package query

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	FilterParam                = "filter"
	SortParam                  = "sort"
	PageParam                  = "page"
	PageSizeParam              = "pagesize"
	RevisionParam              = "revision"
	LimitParam                 = "limit"
	ContinueParam              = "continue"
	ProjectsOrNamespacesParam  = "projectsornamespaces"
	SummaryParam               = "summary"
	SummaryOnlyParam           = "summaryonly"
	SummaryNamespacedParam     = "summarynamespaced"
	IncludeAssociatedDataParam = "includeAssociatedData"
)

// ListOptions models the query parameters supported by Steve collections.
// Filters remain expressions because Steve's grammar is richer than simple
// key/value equality; each entry is encoded as a separate filter parameter.
type ListOptions struct {
	Filters  []string
	Sort     []string
	Page     int
	PageSize int
	Revision string
	// Limit controls Steve's underlying Kubernetes chunk size. Zero omits the
	// parameter, -1 disables Steve's default limit, and positive values bound it.
	Limit int
	// Continue resumes the next Kubernetes chunk using Steve's opaque token.
	Continue                    string
	ProjectsOrNamespaces        []string
	ExcludeProjectsOrNamespaces []string
	Summary                     []string
	SummaryOnly                 *bool
	SummaryNamespaced           *bool
	IncludeAssociatedData       *bool
}

func (options ListOptions) Values() (url.Values, error) {
	if options.Page < 0 {
		return nil, fmt.Errorf("page must not be negative")
	}
	if options.PageSize < 0 {
		return nil, fmt.Errorf("pagesize must not be negative")
	}
	if options.Limit < -1 {
		return nil, fmt.Errorf("limit must be -1, zero, or positive")
	}
	if len(options.ProjectsOrNamespaces) > 0 && len(options.ExcludeProjectsOrNamespaces) > 0 {
		return nil, fmt.Errorf("projectsornamespaces include and exclude cannot be combined")
	}
	if options.SummaryOnly != nil && len(options.Summary) == 0 {
		return nil, fmt.Errorf("summaryonly requires at least one summary field")
	}
	if options.SummaryNamespaced != nil && len(options.Summary) == 0 {
		return nil, fmt.Errorf("summarynamespaced requires at least one summary field")
	}

	values := make(url.Values)
	for _, filter := range options.Filters {
		if strings.TrimSpace(filter) == "" {
			return nil, fmt.Errorf("filter expression must not be empty")
		}
		values.Add(FilterParam, filter)
	}
	if len(options.Sort) > 0 {
		values.Set(SortParam, strings.Join(options.Sort, ","))
	}
	if options.Page > 0 {
		values.Set(PageParam, strconv.Itoa(options.Page))
	}
	if options.PageSize > 0 {
		values.Set(PageSizeParam, strconv.Itoa(options.PageSize))
	}
	if options.Revision != "" {
		values.Set(RevisionParam, options.Revision)
	}
	if options.Limit != 0 {
		values.Set(LimitParam, strconv.Itoa(options.Limit))
	}
	if options.Continue != "" {
		values.Set(ContinueParam, options.Continue)
	}
	if len(options.ProjectsOrNamespaces) > 0 {
		values.Set(ProjectsOrNamespacesParam, strings.Join(options.ProjectsOrNamespaces, ","))
	}
	if len(options.ExcludeProjectsOrNamespaces) > 0 {
		values.Set(ProjectsOrNamespacesParam+"!", strings.Join(options.ExcludeProjectsOrNamespaces, ","))
	}
	if len(options.Summary) > 0 {
		values.Set(SummaryParam, strings.Join(options.Summary, ","))
	}
	setOptionalBool(values, SummaryOnlyParam, options.SummaryOnly)
	setOptionalBool(values, SummaryNamespacedParam, options.SummaryNamespaced)
	setOptionalBool(values, IncludeAssociatedDataParam, options.IncludeAssociatedData)
	return values, nil
}

func (options ListOptions) Encode() (string, error) {
	values, err := options.Values()
	if err != nil {
		return "", err
	}
	return values.Encode(), nil
}

// Equal builds Steve's field=value grammar without adding quotes or escaping.
// Its semantics are mode-dependent: the non-SQL list processor performs a
// substring match, while SQL-backed Steve treats it as equality. Keeping the
// value unquoted is intentional because currently released non-SQL processors
// treat quote characters literally, despite older Steve documentation calling
// single quotes an exact-match operator. Callers should pass grammar-safe values
// and verify exact matches client-side when behavior must be portable.
func Equal(field string, value string) string {
	return field + "=" + value
}

// NotEqual is the negated counterpart to Equal and has the same mode-dependent
// substring-versus-equality behavior and grammar-safety requirements.
func NotEqual(field string, value string) string {
	return field + "!=" + value
}

// In builds Steve's SQL-backed set-membership grammar. Current non-SQL Steve
// does not support this operator. Steve's set parser accepts only unquoted
// identifiers, so values that cannot be represented without changing the
// grammar are rejected. Accepted values are deduplicated and sorted so
// equivalent sets produce the same query.
func In(field string, values ...string) (string, error) {
	return setMembershipExpression(field, "in", values)
}

// NotIn is the negated SQL-backed set-membership counterpart to In.
func NotIn(field string, values ...string) (string, error) {
	return setMembershipExpression(field, "notin", values)
}

func Contains(field string, value string) string {
	return field + " CONTAINS " + strconv.Quote(value)
}

func NotContains(field string, value string) string {
	return field + " NOTCONTAINS " + strconv.Quote(value)
}

func Bool(value bool) *bool {
	return &value
}

func setMembershipExpression(field string, operator string, values []string) (string, error) {
	if err := validateFilterField(field); err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", fmt.Errorf("%s requires at least one value", operator)
	}

	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateSetMembershipValue(value); err != nil {
			return "", fmt.Errorf("%s value %q: %w", operator, value, err)
		}
		unique[value] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for value := range unique {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)

	return field + " " + operator + " (" + strings.Join(ordered, ",") + ")", nil
}

func validateSetMembershipValue(value string) error {
	if value == "" {
		return fmt.Errorf("must not be empty")
	}
	for index, character := range value {
		if index == 0 && !isASCIIFilterIdentifierStart(character) {
			return fmt.Errorf("must start with an ASCII letter, digit, or underscore")
		}
		if character > unicode.MaxASCII {
			return fmt.Errorf("must contain only ASCII characters")
		}
		if unicode.IsSpace(character) || unicode.IsControl(character) || strings.ContainsRune(",=!~<>()'\"\\", character) {
			return fmt.Errorf("contains unsupported Steve set-grammar character %q", character)
		}
	}
	return nil
}

func isASCIIFilterIdentifierStart(character rune) bool {
	return (character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z') ||
		(character >= '0' && character <= '9') ||
		character == '_'
}

func validateFilterField(field string) error {
	if field == "" {
		return fmt.Errorf("filter field must not be empty")
	}
	for _, character := range field {
		if unicode.IsSpace(character) || unicode.IsControl(character) || strings.ContainsRune(",=!~<>()'\"\\", character) {
			return fmt.Errorf("filter field %q contains invalid grammar character %q", field, character)
		}
	}
	return nil
}

func setOptionalBool(values url.Values, key string, value *bool) {
	if value != nil {
		values.Set(key, strconv.FormatBool(*value))
	}
}
