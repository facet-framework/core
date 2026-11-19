# FACET Anchor Guide

`@facet:` tags are source-level annotations that link code to feature records. They allow the FACET linter to verify that every `active` record has at least one implementing file — and that every tag in source resolves to a known record.

---

## Tag syntax

```
@facet: <domain>/<capability>/<feature>
@facet-role: primary | related | config | data
```

Both tags appear as comments in the language's native style. `@facet:` comes first, `@facet-role:` immediately after on the next line.

### Roles

| Role | When to use |
|------|-------------|
| `primary` | The main implementation file (handler, service, core function) |
| `related` | A file that touches the feature but is not the main implementation |
| `config` | Configuration, feature flags, environment wiring |
| `data` | Data models, schema definitions, migration files |

Use `primary` on the one or two files that are the authoritative implementation. Use `related` freely on supporting files.

---

## Placement per language

### Go

Place the tag in the block comment or line comment immediately before the function, struct, or method that is the primary entry point.

```go
// @facet: payments/checkout/stripe-session
// @facet-role: primary
func CreateCheckoutSession(ctx context.Context, req *CheckoutRequest) (*Session, error) {
    ...
}
```

For a struct that owns the feature:

```go
// @facet: auth/webauthn/credential-registration
// @facet-role: primary
type WebAuthnService struct {
    ...
}
```

### Rust

```rust
// @facet: vault/crypto/double-ratchet
// @facet-role: primary
pub fn ratchet_encrypt(state: &mut RatchetState, plaintext: &[u8]) -> Vec<u8> {
    ...
}
```

For a struct implementation:

```rust
// @facet: vault/crypto/double-ratchet
// @facet-role: primary
impl DoubleRatchet {
    ...
}
```

### TypeScript / TSX

```typescript
// @facet: notifications/email/delivery-status
// @facet-role: primary
export async function getDeliveryStatus(messageId: string): Promise<DeliveryStatus> {
    ...
}
```

React component:

```tsx
// @facet: dashboard/charts/metric-panel
// @facet-role: primary
export function MetricPanel({ metric }: MetricPanelProps) {
    ...
}
```

### Python

```python
# @facet: analytics/pipeline/event-ingestion
# @facet-role: primary
def ingest_event(event: Event) -> None:
    ...
```

### PHP

Docblock or line comment, directly above the declaration. Inside a docblock
the tag line starts with ` * ` like every other line of it, and the closing
`*/` ends the block — the scanner reads the tag, and the private-anchor filter
lifts the tag lines out together with the empty ` *` line that sets them off
from the description, and puts both back:

```php
<?php

namespace App\Billing;

/**
 * Creates an invoice from a closed order.
 *
 * @facet: billing/invoice/create
 * @facet-role: primary
 */
final class CreateInvoice
{
    // @facet: billing/invoice/numbering
    // @facet-role: related
    public function nextNumber(): string { /* … */ }
}
```

`#` comments work the same way. Never place a tag before `<?php`: anything
before the opening tag is output, not code. PHPUnit's `FooTest.php` counts as
test code for `test-only-anchors` and `unanchored-hotspots` wherever it sits.

### SQL (migrations)

```sql
-- @facet: billing/subscriptions/tier-limits
-- @facet-role: data
CREATE TABLE subscription_tiers (
    ...
);
```

### YAML / Terraform / config files

```yaml
# @facet: infra/database/pgbouncer-pooling
# @facet-role: config
pgbouncer:
  pool_mode: transaction
```

```hcl
# @facet: infra/platform/terraform-provisioning
# @facet-role: primary
resource "aws_rds_cluster" "main" {
  ...
}
```

### Shell scripts

```sh
# @facet: infra/cicd/facet-lint-tooling
# @facet-role: primary
validate_schemas() {
    ...
}
```

---

## Where to place tags in a file

**Top of file** — when the entire file implements one feature:

```go
// @facet: payments/checkout/stripe-session
// @facet-role: primary

package checkout
```

Leave the empty line. In Go, a comment that touches the `package` clause is the
package's documentation: without the empty line, `go doc` and pkg.go.dev open
the package with `@facet: …` lines, and a real package comment below the tags
is run together with them. If the file also carries the package comment, the
tag block comes first, then the empty line, then the package comment directly
above `package`. The same holds for any language whose tooling reads the
comment directly above a declaration as its documentation: keep the tag block a
paragraph of its own. Above a documented Go symbol, an empty line between the
tag block and the doc comment keeps the tags out of the symbol's documentation
altogether; an empty comment line (`//`) keeps them in it as a separate
paragraph. The linter finds the
tag in either position; the empty line only decides what readers of the
generated documentation see.

**Before the relevant function/struct** — when a file spans multiple features (prefer splitting, but this is valid):

```go
// @facet: payments/checkout/stripe-session
// @facet-role: primary
func CreateSession(...) { ... }

// @facet: payments/checkout/session-expiry
// @facet-role: related
func ExpireSession(...) { ... }
```

---

## Opting a file out of scanning

A file that *shows* the tag syntax rather than using it — a test fixture building sample records, a scaffolding template, a generator that emits tags — would otherwise register anchors for features it does not implement.

Put `@facet-ignore-file` within the first ten lines of such a file and the linter skips it entirely:

```go
// This file builds fixtures containing example @facet: tags.
// @facet-ignore-file

package registry_test
```

Prose formats (`.md`, `.rst`, `.adoc`, `.txt`, and similar) are skipped automatically — an anchor in documentation is always an illustration.

The directive is matched against the first ten lines of file content. A literal there — including in code implementing or checking the marker — opts the file out. Build the string from parts in that case:

```go
var ignoreMarker = []byte("@facet-ignore" + "-file")
```

This is the only place the concession is needed, and FACET's own scanner and language server both use it.

---

## Keeping whole file types out of the scan

`@facet-ignore-file` is a per-file decision and needs somewhere to write the marker, which rules out everything that is not text. A repository is usually a third media by file count — rendered audio, images, fonts, compiled output — and every one of those files is opened and read on every scan to look for a tag it cannot contain.

Put those patterns in `.facetignore` at the repository root:

```
*.mp3
*.png
*.woff2
vendor/
```

Syntax is `.gitignore`'s, one pattern per line, applying to the directory it sits in and everything below. FACET reads it in addition to `.gitignore`.

Three things worth knowing:

- **It is not `.gitignore`.** Excluding a file from the scan says nothing about whether it is versioned, which is the whole point: assets belong in the repository and still never need reading.
- **It is FACET's own file, not `.ignore`.** The walker would read `.ignore` too, but that file belongs to ripgrep and its relatives; following it would tie what FACET scans to a setting somebody made for a search tool.
- **Records are never at risk.** They are loaded by their own directory walk, so no pattern here can hide one, not even `*.yaml`. The failure mode is the opposite: exclude a file that does carry anchors and its records become orphans, which the linter reports.

The scaffold (`facet init`, or the plugin's `facet-init`) writes the file empty, because what is irrelevant is a judgement about the repository rather than something a scaffold can know.

---

## Private anchors — tags that never reach a commit

For a repository you do not own, or whose team has not adopted FACET, the
anchors have to be in the source for the registry to work and cannot be in the
source. `facet filter install` resolves that per clone:

```sh
facet filter install
```

It configures a git clean/smudge filter in `.git/config`, writes `* filter=facet`
to `.git/info/attributes`, and keeps a sidecar under `.git/facet/anchors/`.
Nothing in the repository changes. From then on an `@facet:` block you add on
its own lines is lifted out on the way to the index and put back on the way to
the working copy: `git diff` is empty, no commit or push carries it, and no
merge or rebase can conflict on it, because the lines are never there.

Placement is by content. A block sits above a particular line and follows that
line through a pull that moves it. When a pull removes the line, the block is
kept aside and `facet filter status` names it; restore the line and the block
comes back. Anchors the repository already commits pass through untouched.

Three things to know:

- **`git status` may disagree with `git diff`** right after you add an anchor.
  Git judges a file modified by its size before it runs the filter. `git add
  <file>` or `facet filter refresh` clears it; inside a session the write hook
  does so on its own.
- **Line numbers.** The committed file is shorter than the one in your editor
  by the anchor lines above any point. CI output, stack traces and `git blame`
  refer to the committed file.
- **CI and colleagues have no sidecar.** A record anchored only privately lints
  clean in this clone and reports as unanchored anywhere else. This is a tool
  for documenting code you cannot change, not for a shared registry.

`facet filter uninstall` removes what install wrote. The private anchors stay in
the working copy and are ordinary content from then on.

## Files that must not carry anchors

Do not anchor files that are distributed to be copied into other repositories — vendored tooling, scaffolding templates, example snippets. An anchor is a claim that *this* repository implements *this* record; once the file is copied elsewhere, that claim is false and the receiving project's linter fails on a record it never had.

FACET's own `tools/facet-lint/` scripts are the worked example: they are copied verbatim into every consuming project, so they carry no anchors, even though the linter is a real capability of this repository.

---

## Repositories with variants

When a repository declares variants — a web and a native client, or a legacy
system and its rewrite — **the tags do not change**. There is no
`@facet-variant:` annotation. A file's variant comes from its path, via
`docs/facets/variants.yaml`.

That is deliberate: an annotation could contradict the directory a file sits in,
and then two sources disagree about the same file. A path cannot lie about where
it is.

One rule has real consequences for where you put anchors: **shared code proves
no variant.** A variant counts as implemented only when an anchor sits in a path
belonging to exactly one variant. Anchor shared code anyway — it is part of the
feature and resolves for navigation and drift detection — but if a feature is
marked `active` on a client, anchor that client's own code too, or the linter
will ask whether anyone can actually reach it.

Full guidance: [`VARIANTS-GUIDE.md`](VARIANTS-GUIDE.md).

In a monorepo declaring several registries, a tag may name the one it
implements for — `@facet: storefront:billing/checkout/coupons` — which matters
only in code the registries share: [`REGISTRIES-GUIDE.md`](REGISTRIES-GUIDE.md).

---

## How many tags per feature

- At least **one `primary` tag** is required for a record to be `lifecycle: active`.
- There is no upper limit. A cross-cutting feature (logging, tracing, auth middleware) may have dozens of `related` tags.
- A single file may carry tags for multiple features.

---

## Lint detection

The `check-orphan-ids.sh` script scans the whole repository for `@facet:` tags, skipping build and vendor directories (`node_modules`, `target`, `dist`, `.venv`, and similar). It covers the common source extensions for every language in this guide plus most others.

Two environment variables adjust the scan without editing any script:

| Variable | Effect |
|----------|--------|
| `FACET_SOURCE_DIRS` | Space-separated directories to scan, relative to the repo root. Narrows the sweep in a large monorepo. |
| `FACET_SOURCE_EXTS` | Space-separated file extensions to consider. |

Set the same values in your CI workflow so local runs and CI agree.

The regex requires the three-segment format — single-word tags like `@facet: auth` are silently ignored:

```
@facet:[[:space:]]*[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]+
```

---

## Transitioning a record from `planned` to `active`

1. Add `@facet: <id>` + `@facet-role: primary` to the implementing file(s).
2. Change `lifecycle: planned` → `lifecycle: active` in the record YAML.
3. Run `sh tools/facet-lint/check-orphan-records.sh` — it should no longer report the record.
4. Include both changes in the same PR.
