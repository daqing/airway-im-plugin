<?php

declare(strict_types=1);

// Zero-dependency test runner: `php test/run.php` (no PHPUnit, mirroring
// the Ruby suite's stdlib-only minitest approach). Exits non-zero on any
// failure.

require __DIR__ . '/test_helper.php';

foreach (glob(__DIR__ . '/*_test.php') ?: [] as $file) {
    require_once $file;
}

$failures = [];
$tests = 0;
foreach (get_declared_classes() as $class) {
    if (!is_subclass_of($class, TestCase::class)) {
        continue;
    }
    foreach (get_class_methods($class) as $method) {
        if (!str_starts_with($method, 'test')) {
            continue;
        }
        $tests++;
        $instance = new $class();
        try {
            $instance->{$method}();
            echo "ok   {$class}::{$method}\n";
        } catch (\Throwable $e) {
            $failures[] = "{$class}::{$method}: " . get_class($e) . ': ' . $e->getMessage();
            echo "FAIL {$class}::{$method}: " . get_class($e) . ': ' . $e->getMessage() . "\n";
        } finally {
            $instance->teardown();
        }
    }
}

echo "\n" . ($tests - count($failures)) . "/{$tests} passed\n";
exit($failures === [] ? 0 : 1);
