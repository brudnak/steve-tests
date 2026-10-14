package pods

import (
	"strings"
	"testing"
	"time"
)

func TestPodLastTerminationTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		times   []string
		want    string
		wantErr string
	}{
		{name: "latest container termination", times: []string{"2026-08-05T12:00:01Z", "2026-08-05T12:00:03.5Z"}, want: "2026-08-05T12:00:03.5Z"},
		{name: "missing statuses"},
		{name: "invalid timestamp", times: []string{"not-a-time"}, wantErr: "not-a-time"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := map[string]any{}
			if test.times != nil {
				var statuses []any
				for _, timestamp := range test.times {
					statuses = append(statuses, map[string]any{"lastState": map[string]any{"terminated": map[string]any{"finishedAt": timestamp}}})
				}
				item["status"] = map[string]any{"containerStatuses": statuses}
			}
			got, found, err := podLastTerminationTime(item)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if found != (test.want != "") {
				t.Fatalf("found = %t, want %t", found, test.want != "")
			}
			if found && got.Format(time.RFC3339Nano) != test.want {
				t.Fatalf("termination = %s, want %s", got.Format(time.RFC3339Nano), test.want)
			}
		})
	}
}
