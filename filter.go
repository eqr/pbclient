package pbclient

import (
	"fmt"
	"strings"
)

// Eq builds an equality filter: field='value'.
func Eq(field, value string) string {
	return fmt.Sprintf("%s='%s'", field, escapeFilterValue(value))
}

// Neq builds a not-equal filter: field!='value'.
func Neq(field, value string) string {
	return fmt.Sprintf("%s!='%s'", field, escapeFilterValue(value))
}

// Gt builds a greater-than filter: field>value.
func Gt(field, value string) string {
	return fmt.Sprintf("%s>%s", field, strings.TrimSpace(value))
}

// Gte builds a greater-than-or-equal filter: field>=value.
func Gte(field, value string) string {
	return fmt.Sprintf("%s>=%s", field, strings.TrimSpace(value))
}

// Lt builds a less-than filter: field<value.
func Lt(field, value string) string {
	return fmt.Sprintf("%s<%s", field, strings.TrimSpace(value))
}

// Lte builds a less-than-or-equal filter: field<=value.
func Lte(field, value string) string {
	return fmt.Sprintf("%s<=%s", field, strings.TrimSpace(value))
}

// And joins filters with logical AND, skipping empty entries.
func And(filters ...string) string {
	return combineFilters("&&", filters...)
}

// Or joins filters with logical OR, skipping empty entries.
func Or(filters ...string) string {
	return combineFilters("||", filters...)
}

func combineFilters(op string, filters ...string) string {
	clean := make([]string, 0, len(filters))
	for _, f := range filters {
		f = strings.TrimSpace(f)
		if f != "" {
			clean = append(clean, f)
		}
	}

	switch len(clean) {
	case 0:
		return ""
	case 1:
		return clean[0]
	default:
		return "(" + strings.Join(clean, " "+op+" ") + ")"
	}
}

func escapeFilterValue(value string) string {
	return strings.ReplaceAll(value, "'", "\\'")
}
