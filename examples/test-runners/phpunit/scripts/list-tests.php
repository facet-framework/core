<?php

declare(strict_types=1);

// PHPUnit writes its machine-readable discovery to a file. Keep console output
// on stderr, preserve the process exit, and forward explicit file attributes.
$report = tempnam(sys_get_temp_dir(), 'facet-phpunit-list-');
if ($report === false) {
    fwrite(STDERR, "Cannot allocate discovery report\n");
    exit(1);
}
$exit = 1;
try {
    $process = proc_open(
        [PHP_BINARY, __DIR__ . '/../vendor/bin/phpunit', '--no-logging', '--list-tests-xml', $report],
        [0 => STDIN, 1 => STDERR, 2 => STDERR],
        $pipes,
        dirname(__DIR__)
    );
    if (!is_resource($process)) {
        throw new RuntimeException('Cannot start PHPUnit discovery');
    }
    $exit = proc_close($process);
    if ($exit === 0) {
        $xml = simplexml_load_file($report);
        if ($xml === false) {
            throw new RuntimeException('Invalid PHPUnit discovery XML');
        }
        $files = [];
        foreach ($xml->xpath('//*[@file]') as $entry) {
            $files[(string) $entry['file']] = true;
        }
        foreach (array_keys($files) as $file) {
            echo $file, "\n";
        }
    }
} catch (Throwable $error) {
    fwrite(STDERR, $error->getMessage() . "\n");
    $exit = 1;
} finally {
    unlink($report);
}
exit($exit);
