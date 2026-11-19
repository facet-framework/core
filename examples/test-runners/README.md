# Test runner examples

These fictional projects connect two arithmetic features to Jest, Vitest or
PHPUnit tests. Each project includes its own FACET records, runner declaration,
source and tests. Four selected tests pass; a fifth deliberately failing test
is excluded by the runner configuration.

FACET runs the commands declared by the project. Install its dependencies and
configure its reporter before asking FACET to execute tests.

## Run an example

Build the Core binary as described in the repository README. From the Core
repository root, install dependencies for the example you want to try:

```sh
pnpm --dir examples/test-runners/jest install --frozen-lockfile
pnpm --dir examples/test-runners/vitest install --frozen-lockfile
composer --working-dir=examples/test-runners/phpunit install --no-interaction --prefer-dist
```

The JavaScript examples declare their Node.js requirements and pnpm version in
`package.json`. The PHP example uses PHP 8.2 or later with DOM/XML and mbstring,
Composer 2 and PHPUnit 11. The committed lockfiles select dependency versions.
Only the tools for your chosen example are required.

Run the corresponding command:

```sh
./dist/facet --root examples/test-runners/jest test
./dist/facet --root examples/test-runners/vitest test
./dist/facet --root examples/test-runners/phpunit test
```

Each successful run reports two passed features. Copy a complete example into
another directory to try a standalone consumer project; its configuration,
record references and tests use paths local to that project.

## Understand the declaration

Read `.facetrc.yaml` in each example alongside its reporter configuration:

| Example | File discovery | Execution evidence |
| --- | --- | --- |
| Jest | `jest --listTests --runInBand --json` | `jest-junit`, configured in `jest.config.cjs` |
| Vitest | `vitest list --filesOnly --json` | Built-in JUnit reporter in `vitest.config.js` |
| PHPUnit | `php scripts/list-tests.php` | JUnit logging in `phpunit.xml` |

The discovery command must select the same files as the execution command.
JUnit evidence must include file paths; test names and class names do not
substitute for paths. The PHP discovery helper parses explicit XML file
attributes and removes its temporary report.

From the repository root, inspect discovery or machine-readable results with:

```sh
./dist/facet --root examples/test-runners/jest test --check
./dist/facet --root examples/test-runners/jest test --json
./dist/facet --root examples/test-runners/jest test --fail-unmeasured
```

`--check` discovers files and checks references without executing tests.
`--json` writes the FACET result to stdout and runner output to stderr.
`--fail-unmeasured` requires usable evidence for every selected runner file and
feature. It requires execution and cannot be combined with `--check` or `--list`.
Skipped tests, unusable reports and missing references remain `unmeasured`.
A runner's nonzero exit remains a failure regardless of that strict option.

## Observe a failure

In the chosen JavaScript example's `src/arithmetic.js`, replace `return a + b;`
with `return a - b;`. In PHP, make the corresponding change in
`src/Arithmetic.php`. Rerun that example: addition fails while multiplication
continues to pass. Restore the source and rerun to return to passing results.

Records associate test files with features and scenarios. Passing a file does
not establish that every named scenario has been proved. New test files need
reviewed record references or reviewed baseline entries; `--update-baseline`
only removes stale entries and never accepts new unclaimed tests.
