package actions

import "testing"

func TestWorkloadImageUsesTrimmedOverrideAndPinnedDefault(t *testing.T) {
	tests := []struct{ name, override, want string }{
		{name: "default", want: DefaultWorkloadImage},
		{name: "whitespace", override: "   ", want: DefaultWorkloadImage},
		{name: "trimmed override", override: "  registry.example.test/qa/busybox:v1  ", want: "registry.example.test/qa/busybox:v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(EnvTestPodImage, test.override)
			if got := WorkloadImage(); got != test.want {
				t.Fatalf("WorkloadImage() = %q, want %q", got, test.want)
			}
		})
	}
}
