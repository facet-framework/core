'use strict';
// @facet: delivery/distribution/npm-launcher
// @facet-role: primary

const fs = require('fs');
const os = require('os');
const path = require('path');
const childProcess = require('child_process');

// npm's process.platform-process.arch, as in the platform package names.
const SUPPORTED = ['linux-x64', 'linux-arm64', 'darwin-x64', 'darwin-arm64', 'win32-x64', 'win32-arm64'];
const FORWARDED_SIGNALS = ['SIGINT', 'SIGTERM', 'SIGHUP'];
// Signals Node acts on itself instead of dying: SIGUSR1 starts the inspector,
// SIGPIPE is ignored. Raising one on this process would not end it the way it
// ended the binary, so the launcher exits 128+n instead.
const NODE_HANDLED_SIGNALS = ['SIGUSR1', 'SIGPIPE'];
const EXIT_LAUNCHER_ERROR = 1;

class LauncherError extends Error {}

function binaryName(platform) {
  return platform === 'win32' ? 'facet.exe' : 'facet';
}

// Finds the binary for platform/arch or throws a LauncherError whose message
// names what is wrong and how to repair it.
function locateBinary(options) {
  const { launcherPackage, platform, arch, resolve, command } = options;
  const key = `${platform}-${arch}`;
  if (!SUPPORTED.includes(key)) {
    throw new LauncherError(
      `${command}: this platform (${key}) is not supported. ` +
        `Native binaries exist for: ${SUPPORTED.join(', ')}.`
    );
  }
  const platformPackage = `${launcherPackage}-${key}`;
  let manifestPath;
  try {
    manifestPath = resolve(`${platformPackage}/package.json`);
  } catch (err) {
    if (err && err.code !== 'MODULE_NOT_FOUND') throw err;
    throw new LauncherError(
      `${command}: the platform package ${platformPackage} is not installed.\n` +
        `It is an optional dependency of ${launcherPackage}, and npm skips optional dependencies ` +
        `when they are omitted (--omit=optional, --no-optional, or omit=optional in the npm ` +
        `configuration or environment). Reinstall without omitting optional dependencies, for example ` +
        `'npm install' in this project, or 'npm install --include=optional'.\n` +
        `It is also missing when node_modules was installed on another platform or architecture ` +
        `and copied or mounted here; npm installs only the platform package of the machine it ran on, ` +
        `so run the install again on this machine.`
    );
  }
  const binary = path.join(path.dirname(manifestPath), binaryName(platform));
  let stat;
  try {
    stat = fs.statSync(binary);
  } catch (err) {
    throw new LauncherError(
      `${command}: ${platformPackage} is installed, but its binary is missing: ${binary}\n` +
        `The package is incomplete; remove it and install it again.`
    );
  }
  if (!stat.isFile()) {
    throw new LauncherError(`${command}: ${binary} in ${platformPackage} is not a file.`);
  }
  if (platform !== 'win32') {
    try {
      fs.accessSync(binary, fs.constants.X_OK);
    } catch (err) {
      throw new LauncherError(
        `${command}: ${binary} in ${platformPackage} is not executable ` +
          `(mode ${(stat.mode & 0o777).toString(8)}). The package was unpacked without its ` +
          `file modes; remove it and install it again.`
      );
    }
  }
  return binary;
}

function signalPolicy(platform) {
  if (platform === 'win32') return { forward: [], reraise: false };
  return { forward: FORWARDED_SIGNALS.slice(), reraise: true };
}

// What the launcher does once the binary has ended: exit with a code, or raise
// a signal on itself (with 128+n as the fallback should it survive it).
function exitPlan(code, signal, policy) {
  if (!signal) return { exit: code === null ? EXIT_LAUNCHER_ERROR : code };
  if (!policy.reraise) return { exit: EXIT_LAUNCHER_ERROR };
  const fallback = 128 + (os.constants.signals[signal] || 0);
  if (NODE_HANDLED_SIGNALS.includes(signal)) return { exit: fallback };
  return { raise: signal, exit: fallback };
}

// Runs the binary with args unchanged and ends this process the way the child
// ended (see exitPlan). options.spawn and options.policy exist for tests only.

function run(binary, args, command, options = {}) {
  const policy = options.policy || signalPolicy(process.platform);
  const spawn = options.spawn || childProcess.spawn;
  const child = spawn(binary, args, { stdio: 'inherit', windowsHide: false });
  let started = false;
  let forwarding = 'a signal';
  child.on('spawn', () => {
    started = true;
  });
  // A failed kill does not end the launcher: the binary is still running and
  // still owns the exit. ESRCH means it is already gone; its exit event follows.
  const reportKillFailure = (signal, err) => {
    if (err && err.code === 'ESRCH') return;
    process.stderr.write(`${command}: could not pass ${signal} on to ${binary}: ${err && err.message}\n`);
  };
  const forwarders = {};
  for (const signal of policy.forward) {
    forwarders[signal] = () => {
      try {
        forwarding = signal;
        child.kill(signal);
      } catch (err) {
        reportKillFailure(signal, err);
      }
    };
    process.on(signal, forwarders[signal]);
  }
  const unhook = () => {
    for (const signal of Object.keys(forwarders)) process.removeListener(signal, forwarders[signal]);
  };
  child.on('error', (err) => {
    // child.kill() reports a failed delivery as an 'error' event with syscall
    // 'kill'; only an error before the process started means it never ran.
    if (started || (err && err.syscall === 'kill')) {
      reportKillFailure(forwarding, err);
      return;
    }
    unhook();
    process.stderr.write(`${command}: could not start ${binary}: ${err.message}\n`);
    process.exit(EXIT_LAUNCHER_ERROR);
  });
  child.on('exit', (code, signal) => {
    unhook();
    const plan = exitPlan(code, signal, policy);
    if (plan.raise) {
      // With our handlers removed the default action applies, so the parent of
      // this process sees the same signal.
      try {
        process.kill(process.pid, plan.raise);
      } catch (err) {
        /** not deliverable; the fallback below exits 128+n */
      }
      setTimeout(() => process.exit(plan.exit), 100).unref();
      process.exitCode = plan.exit;
      return;
    }
    process.exit(plan.exit);
  });
  return child;
}

// main is what the entry packages call. Options exist so a test can name a
// platform this machine is not; nothing in the shipped entry points passes them.
function main(options = {}) {
  const launcherPackage = options.launcherPackage || require('./package.json').name;
  const command = options.command || launcherPackage;
  const args = options.args || process.argv.slice(2);
  let binary;
  try {
    binary = locateBinary({
      launcherPackage,
      command,
      platform: options.platform || process.platform,
      arch: options.arch || process.arch,
      resolve: options.resolve || ((request) => require.resolve(request)),
    });
  } catch (err) {
    if (!(err instanceof LauncherError)) throw err;
    process.stderr.write(err.message + '\n');
    process.exit(EXIT_LAUNCHER_ERROR);
  }
  return run(binary, args, command);
}

module.exports = { main, run, locateBinary, signalPolicy, exitPlan, SUPPORTED, LauncherError };

if (require.main === module) main();
