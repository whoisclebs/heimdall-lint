package validate

import (
	"strings"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/envfile"
	"github.com/whoisclebs/heimdall-lint/internal/naming"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

const rulesSchema = `
version: 1
variables:
  REDIS_ENABLED:  {type: boolean, default: false}
  REDIS_URL:      {type: url, required_if: {REDIS_ENABLED: true}}
  CACHE_MODE:     {type: enum, values: [redis, local], default: local}
  REDIS_TTL:      {type: duration, required_if: {REDIS_ENABLED: true, CACHE_MODE: redis}}
  TLS_CERT:       {type: string, requires: [TLS_KEY]}
  TLS_KEY:        {type: string, secret: true}
  MEMCACHED_URL:  {type: string, conflicts_with: [REDIS_URL]}
  SENTINEL_A:     {type: string, conflicts_with: [SENTINEL_B]}
  SENTINEL_B:     {type: string, conflicts_with: [SENTINEL_A]}
`

func runRules(t *testing.T, env string) Result {
	t.Helper()
	contract, err := schema.Parse([]byte(rulesSchema), "rules.schema.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return New(contract, naming.Spring{}, Options{}).Validate("app.env", "app", envfile.Parse([]byte(env)))
}

func codesFor(result Result, code diag.Code) []string {
	var variables []string
	for _, d := range result.Diagnostics {
		if d.Code == code {
			variables = append(variables, d.Variable)
		}
	}
	return variables
}

func TestRequiredIfHoldsOnlyWhenConditionsMatch(t *testing.T) {
	if got := codesFor(runRules(t, ""), diag.RequiredByCondition); len(got) != 0 {
		t.Fatalf("default false must not trigger: %v", got)
	}
	if got := codesFor(runRules(t, "REDIS_ENABLED=false\n"), diag.RequiredByCondition); len(got) != 0 {
		t.Fatalf("got %v", got)
	}

	result := runRules(t, "REDIS_ENABLED=TRUE\n")
	got := codesFor(result, diag.RequiredByCondition)
	if len(got) != 1 || got[0] != "REDIS_URL" {
		t.Fatalf("got %v (boolean comparison is case-insensitive; REDIS_TTL needs both conditions)", got)
	}
	if d := find(result, diag.RequiredByCondition, "REDIS_URL"); d == nil || d.Message != "Required because REDIS_ENABLED is true." || d.Expected != "absolute URL" {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestRequiredIfWithSeveralConditionsAndEffectiveDefaults(t *testing.T) {
	result := runRules(t, "REDIS_ENABLED=true\nREDIS_URL=redis://x\nCACHE_MODE=redis\n")
	d := find(result, diag.RequiredByCondition, "REDIS_TTL")
	if d == nil || d.Message != "Required because CACHE_MODE is redis and REDIS_ENABLED is true." {
		t.Fatalf("diagnostic = %+v", d)
	}
	if got := codesFor(runRules(t, "REDIS_ENABLED=true\nREDIS_URL=redis://x\nREDIS_TTL=5s\nCACHE_MODE=redis\n"), diag.RequiredByCondition); len(got) != 0 {
		t.Fatalf("satisfied rule reported: %v", got)
	}
}

func TestRequiredIfTreatsEmptyValueAsMissing(t *testing.T) {
	if got := codesFor(runRules(t, "REDIS_ENABLED=true\nREDIS_URL=\n"), diag.RequiredByCondition); len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

func TestRequires(t *testing.T) {
	result := runRules(t, "TLS_CERT=/etc/cert.pem\n")
	d := find(result, diag.MissingDependency, "TLS_KEY")
	if d == nil || d.Message != "Required because TLS_CERT is set." {
		t.Fatalf("diagnostic = %+v", d)
	}
	if got := codesFor(runRules(t, "TLS_CERT=/c\nTLS_KEY=k\n"), diag.MissingDependency); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if got := codesFor(runRules(t, "TLS_KEY=k\n"), diag.MissingDependency); len(got) != 0 {
		t.Fatalf("requires must be one-directional: %v", got)
	}
}

func TestConflictsAreReportedOncePerPair(t *testing.T) {
	result := runRules(t, "REDIS_ENABLED=false\nREDIS_URL=redis://x\nMEMCACHED_URL=m\n")
	got := codesFor(result, diag.ConflictingVariables)
	if len(got) != 1 || got[0] != "MEMCACHED_URL" {
		t.Fatalf("one-sided conflict: %v", got)
	}

	result = runRules(t, "SENTINEL_A=1\nSENTINEL_B=2\n")
	got = codesFor(result, diag.ConflictingVariables)
	if len(got) != 1 || got[0] != "SENTINEL_A" {
		t.Fatalf("mutual conflict must be reported once, on the first name: %v", got)
	}
	if d := find(result, diag.ConflictingVariables, "SENTINEL_A"); d.Message != "Cannot be set together with SENTINEL_B." || d.Line != 1 {
		t.Fatalf("diagnostic = %+v", d)
	}
	if got := codesFor(runRules(t, "SENTINEL_A=1\n"), diag.ConflictingVariables); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestRulesDoNotDoubleReportAnAlreadyMissingVariable(t *testing.T) {
	contract, err := schema.Parse([]byte(`
version: 1
variables:
  A: {type: string, requires: [B]}
  B: {type: string, required: true}
`), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	result := New(contract, naming.Spring{}, Options{}).Validate("f.env", "app", envfile.Parse([]byte("A=1\n")))
	var forB int
	for _, d := range result.Diagnostics {
		if d.Variable == "B" {
			forB++
		}
	}
	if forB != 1 {
		t.Fatalf("B reported %d times: %+v", forB, result.Diagnostics)
	}
}

func TestRuleDiagnosticsNeverLeakSecrets(t *testing.T) {
	result := runRules(t, "TLS_KEY=super-secret-value\nTLS_CERT=/c\n")
	for _, d := range result.Diagnostics {
		if strings.Contains(d.Message+d.Expected+d.Received, "super-secret-value") {
			t.Fatalf("leak in %+v", d)
		}
	}
}
