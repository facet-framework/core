// @facet: delivery/distribution/setup-status
// @facet-role: related
package cli

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/facet-framework/core/registry"
)

// A product linked into the build can add rows to 'facet setup status'. Its
// rows stand before the plugins row, in registration order; a build that links
// no such product reports Core's rows alone.

// The four states a setup row can be in.
const (
	SetupOK            = string(stateOK)
	SetupRed           = string(stateRed)
	SetupUnmeasured    = string(stateUnmeasured)
	SetupNotApplicable = string(stateNotApplicable)
)

// SetupResult is one registry's answer to a product's setup question.
type SetupResult struct {
	// State is SetupOK, SetupRed, SetupUnmeasured or SetupNotApplicable.
	State    string
	Finding  string
	Evidence string
}

// SetupCheck is a row a product adds to the setup report. Check answers for
// one registry; with several registries the worst state wins, as for Core's
// own registry rows.
type SetupCheck struct {
	ID    string
	Check func(reg facet.Registry) SetupResult
}

var productSetupChecks struct {
	sync.Mutex
	checks []SetupCheck
}

// RegisterSetupCheck adds a product's row. Builds register from init. It
// panics on an empty id, a missing check or a repeated id.
func RegisterSetupCheck(check SetupCheck) {
	if check.ID == "" || check.Check == nil {
		panic("cli: RegisterSetupCheck needs an id and a check")
	}
	productSetupChecks.Lock()
	defer productSetupChecks.Unlock()
	for _, existing := range productSetupChecks.checks {
		if existing.ID == check.ID {
			panic(fmt.Sprintf("cli: setup check %q is registered twice", check.ID))
		}
	}
	productSetupChecks.checks = append(productSetupChecks.checks, check)
}

// withProductSetupChecks returns the report's checks with every registered
// product row placed before the plugins row.
func withProductSetupChecks(checks []func(*statusEnv) statusRow) []func(*statusEnv) statusRow {
	productSetupChecks.Lock()
	registered := append([]SetupCheck(nil), productSetupChecks.checks...)
	productSetupChecks.Unlock()
	if len(registered) == 0 {
		return checks
	}
	plugins := reflect.ValueOf(checkPlugins).Pointer()
	out := make([]func(*statusEnv) statusRow, 0, len(checks)+len(registered))
	inserted := false
	for _, check := range checks {
		if !inserted && reflect.ValueOf(check).Pointer() == plugins {
			out = append(out, productRows(registered)...)
			inserted = true
		}
		out = append(out, check)
	}
	if !inserted {
		out = append(out, productRows(registered)...)
	}
	return out
}

func productRows(registered []SetupCheck) []func(*statusEnv) statusRow {
	rows := make([]func(*statusEnv) statusRow, 0, len(registered))
	for _, check := range registered {
		rows = append(rows, func(e *statusEnv) statusRow {
			return overRegistries(e, check.ID, noRegistryHere, func(reg facet.Registry) regResult {
				r := check.Check(reg)
				return regResult{checkState(r.State), r.Finding, r.Evidence}
			})
		})
	}
	return rows
}
