package validate

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/envfile"
	"github.com/whoisclebs/heimdall-lint/internal/naming"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

const testSchema = `
version: 1
variables:
  DATABASE_HOST: {type: hostname, required: true}
  DATABASE_PORT: {type: integer, required: true, min: 1, max: 65535}
  QUEUE_PROVIDER: {type: enum, required: true, values: [KAFKA, RABBITMQ, SQS, NATS]}
  API_TIMEOUT: {type: duration, default: 5s, min: 100ms, max: 30s}
  API_BASE_URL: {type: url, required: true, schemes: [https]}
  API_KEY: {type: string, required: true, secret: true}
  SECRET_PORT: {type: port, secret: true}
  LEGACY_API_URL: {type: string, deprecated: true, replacement: API_BASE_URL}
  LEGACY_FLAG: {type: boolean, deprecated: true}
`

const validEnv = `DATABASE_HOST=db.internal
DATABASE_PORT=5432
QUEUE_PROVIDER=KAFKA
API_BASE_URL=https://api.example.com
API_KEY=abcd-super-secret-value
`

func run(t *testing.T, env string, options Options) Result {
	t.Helper()
	contract, err := schema.Parse([]byte(testSchema), "test.schema.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return New(contract, naming.Spring{}, options).Validate("app.env", "app", envfile.Parse([]byte(env)))
}

func find(result Result, code diag.Code, variable string) *diag.Diagnostic {
	for i, d := range result.Diagnostics {
		if d.Code == code && d.Variable == variable {
			return &result.Diagnostics[i]
		}
	}
	return nil
}

func TestValidEnvironmentHasNoDiagnostics(t *testing.T) {
	result := run(t, validEnv, Options{})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if result.VariablesChecked != 5 {
		t.Fatalf("VariablesChecked = %d", result.VariablesChecked)
	}
}

func TestMissingRequired(t *testing.T) {
	result := run(t, "DATABASE_HOST=db\n", Options{})
	for _, name := range []string{"DATABASE_PORT", "QUEUE_PROVIDER", "API_BASE_URL", "API_KEY"} {
		if find(result, diag.MissingRequired, name) == nil {
			t.Errorf("missing HML001 for %s", name)
		}
	}
	if find(result, diag.MissingRequired, "API_TIMEOUT") != nil {
		t.Error("variable with default must not be required")
	}
}

func TestRequiredButEmpty(t *testing.T) {
	result := run(t, validEnv+"DATABASE_HOST=\n", Options{})
	if d := find(result, diag.MissingRequired, "DATABASE_HOST"); d == nil || !strings.Contains(d.Message, "empty") {
		t.Fatalf("got %+v", d)
	}
}

func TestTypedErrorsCarryExpectedAndReceived(t *testing.T) {
	env := strings.Replace(validEnv, "DATABASE_PORT=5432", "DATABASE_PORT=banana", 1)
	d := find(run(t, env, Options{}), diag.InvalidType, "DATABASE_PORT")
	if d == nil {
		t.Fatal("expected HML003")
	}
	if d.Expected != "integer between 1 and 65535" || d.Received != `"banana"` || d.Line != 2 {
		t.Fatalf("d = %+v", d)
	}
}

func TestRangeAndEnumErrors(t *testing.T) {
	env := validEnv + "API_TIMEOUT=50ms\n"
	if find(run(t, env, Options{}), diag.BelowMinimum, "API_TIMEOUT") == nil {
		t.Error("expected HML005")
	}
	env = validEnv + "API_TIMEOUT=2m\n"
	if find(run(t, env, Options{}), diag.AboveMaximum, "API_TIMEOUT") == nil {
		t.Error("expected HML006")
	}
	env = strings.Replace(validEnv, "QUEUE_PROVIDER=KAFKA", "QUEUE_PROVIDER=KAFKAA", 1)
	d := find(run(t, env, Options{}), diag.InvalidEnum, "QUEUE_PROVIDER")
	if d == nil || !slices.Equal(d.Suggestions, []string{"KAFKA"}) {
		t.Fatalf("enum diagnostic = %+v", d)
	}
}

func TestUnknownVariablesAndSuggestions(t *testing.T) {
	env := validEnv + "API_TIMOUT=5000\napi.timeout=5000\nCOMPLETELY_OTHER=1\n"
	result := run(t, env, Options{})

	typo := find(result, diag.UnknownVariable, "API_TIMOUT")
	if typo == nil || !slices.Equal(typo.Suggestions, []string{"API_TIMEOUT"}) || typo.SuggestionKind != diag.DidYouMean {
		t.Fatalf("typo = %+v", typo)
	}
	spring := find(result, diag.UnknownVariable, "api.timeout")
	if spring == nil || !strings.Contains(spring.Message, "Spring property notation") ||
		!slices.Equal(spring.Suggestions, []string{"API_TIMEOUT"}) || spring.SuggestionKind != diag.Use {
		t.Fatalf("spring = %+v", spring)
	}
	if other := find(result, diag.UnknownVariable, "COMPLETELY_OTHER"); other == nil || len(other.Suggestions) != 0 {
		t.Fatalf("unrelated = %+v", other)
	}
}

func TestAllowUnknown(t *testing.T) {
	result := run(t, validEnv+"WHATEVER=1\n", Options{AllowUnknown: true})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestDeprecatedIsAWarning(t *testing.T) {
	result := run(t, validEnv+"LEGACY_API_URL=x\nLEGACY_FLAG=true\n", Options{})
	d := find(result, diag.DeprecatedVariable, "LEGACY_API_URL")
	if d == nil || d.Severity != diag.SeverityWarning || !slices.Equal(d.Suggestions, []string{"API_BASE_URL"}) || d.SuggestionKind != diag.Use {
		t.Fatalf("d = %+v", d)
	}
	if d := find(result, diag.DeprecatedVariable, "LEGACY_FLAG"); d == nil || len(d.Suggestions) != 0 {
		t.Fatalf("no-replacement deprecation = %+v", d)
	}
	if diag.Count(result.Diagnostics, diag.SeverityError) != 0 {
		t.Fatal("deprecation must not be an error")
	}
}

func TestDuplicatesAndSyntaxAreErrors(t *testing.T) {
	result := run(t, validEnv+"DATABASE_PORT=1\nBROKEN LINE\n", Options{})
	if d := find(result, diag.DuplicateVariable, "DATABASE_PORT"); d == nil || d.Severity != diag.SeverityError {
		t.Fatalf("duplicate = %+v", d)
	}
	found := false
	for _, d := range result.Diagnostics {
		found = found || d.Code == diag.InvalidEnvSyntax
	}
	if !found {
		t.Fatal("expected HML013")
	}
}

func TestSecretsAreNeverRendered(t *testing.T) {
	const secret = "abcd-super-secret-value"
	env := validEnv +
		"SECRET_PORT=" + secret + "\n" + // wrong type on a secret
		"LEGACY_API_URL=" + secret + "\n" + // deprecated
		"API_KEY=" + secret + "\n" + // duplicate of a secret
		"UNKNOWN_" + secret + "=1\n" +
		"BROKEN" + secret + "\n" // syntax error

	result := run(t, env, Options{})
	if d := find(result, diag.InvalidType, "SECRET_PORT"); d == nil || d.Received != diag.Redacted {
		t.Fatalf("secret received = %+v", d)
	}
	for _, d := range result.Diagnostics {
		// Names of unknown variables and syntax lines are user text, not values.
		if d.Code == diag.UnknownVariable || d.Code == diag.InvalidEnvSyntax {
			continue
		}
		if strings.Contains(fmt.Sprintf("%+v", d), secret) {
			t.Fatalf("secret leaked in %+v", d)
		}
	}
}

func TestReceivedEscapesTerminalSequences(t *testing.T) {
	env := strings.Replace(validEnv, "DATABASE_PORT=5432", "DATABASE_PORT=\"\\x1b[31mred\"", 1)
	d := find(run(t, env, Options{}), diag.InvalidType, "DATABASE_PORT")
	if d == nil || strings.ContainsRune(d.Received, 0x1b) {
		t.Fatalf("d = %+v", d)
	}
}

func TestOutputIsSortedAndStable(t *testing.T) {
	env := "B_UNKNOWN=1\nA_UNKNOWN=1\nDATABASE_PORT=x\n"
	first := run(t, env, Options{})
	for range 20 {
		again := run(t, env, Options{})
		if fmt.Sprint(again.Diagnostics) != fmt.Sprint(first.Diagnostics) {
			t.Fatal("diagnostics order is not stable")
		}
	}
}
