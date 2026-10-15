package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/digocelos/transaction-risk-go/internal/synthetic"
)

func synthesize(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// synthesize writes the dataset the generator produces for its flags and
// says on stderr that it is synthetic.
func TestSynthesizeWritesTheGeneratedDataset(t *testing.T) {
	code, stdout, stderr := synthesize(t, "-records", "200", "-seed", "9", "-fraud-rate", "0.05")
	if code != 0 {
		t.Fatalf("exit code = %d; stderr:\n%s", code, stderr)
	}

	options := synthetic.DefaultOptions(200, 9)
	options.FraudRate = 0.05
	var want bytes.Buffer
	if err := synthetic.Generate(&want, options); err != nil {
		t.Fatal(err)
	}

	if stdout != want.String() {
		t.Error("stdout is not the generated dataset")
	}
	if strings.Count(stdout, "\n") != 200 {
		t.Errorf("stdout has %d lines, want 200", strings.Count(stdout, "\n"))
	}
	if !strings.Contains(stderr, "synthetic") || !strings.Contains(stderr, "not real") {
		t.Errorf("stderr does not label the data as synthetic:\n%s", stderr)
	}
}

func TestSynthesizeDefaultFraudRate(t *testing.T) {
	_, stdout, _ := synthesize(t, "-records", "50", "-seed", "2")

	var want bytes.Buffer
	if err := synthetic.Generate(&want, synthetic.DefaultOptions(50, 2)); err != nil {
		t.Fatal(err)
	}
	if stdout != want.String() {
		t.Error("the default fraud rate is not the generator's default")
	}
}

func TestSynthesizeRejectsInvalidArguments(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"-seed", "1"}, "missing required flags: -records"},
		{[]string{"-records", "10"}, "missing required flags: -seed"},
		{[]string{"-records", "0", "-seed", "1"}, "records must be at least 1"},
		{[]string{"-records", "10", "-seed", "1", "-fraud-rate", "1"}, "fraud rate"},
		{[]string{"-records", "ten", "-seed", "1"}, "invalid value"},
		{[]string{"-records", "10", "-seed", "1", "extra"}, `unexpected argument "extra"`},
	}

	for _, c := range cases {
		code, stdout, stderr := synthesize(t, c.args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, c.want) || strings.Contains(stderr, "writing") {
			t.Errorf("%v: exit code %d, stdout %q, stderr:\n%s", c.args, code, stdout, stderr)
		}
	}

	if code, _, stderr := synthesize(t, "-h"); code != 0 || !strings.Contains(stderr, "usage: synthesize") {
		t.Errorf("-h: exit code %d, stderr:\n%s", code, stderr)
	}
}
