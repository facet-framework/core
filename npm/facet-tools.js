#!/usr/bin/env node
'use strict';
// @facet: delivery/distribution/npm-launcher
// @facet-role: related
//
// The facet-tools entry point, reserved for further FACET products; none is
// available through npm yet. It exposes only the 'facet-tools' command, never
// 'facet', so it installs globally beside facet-framework without a bin
// collision.

const COMMAND = 'facet-tools';

const HELP = `Usage: facet-tools <command> [arguments...]

facet-tools is reserved for further FACET products. None is available through
npm yet, so this package runs no product command today.

Options:
  --help, -h     Show this help.
  --version, -v  Print the version of the facet-tools package.

The FACET Core command line is the separate package facet-framework.
`;

function main(args) {
  const [subcommand] = args;
  if (subcommand === undefined) {
    process.stderr.write(HELP);
    process.exit(2);
  }
  if (subcommand === '--help' || subcommand === '-h' || subcommand === 'help') {
    process.stdout.write(HELP);
    return;
  }
  if (subcommand === '--version' || subcommand === '-v') {
    process.stdout.write(require('./package.json').version + '\n');
    return;
  }
  process.stderr.write(`${COMMAND}: unknown command '${subcommand}'\n\n${HELP}`);
  process.exit(2);
}

main(process.argv.slice(2));
