<?php

declare(strict_types=1);

// Autoloader for projects not using Composer. Composer users get the same
// mapping from composer.json (PSR-4: AirwayIM\ => src/).
//
//   require '/path/to/airway-im-sdk-php/src/autoload.php';

spl_autoload_register(static function (string $class): void {
    $prefix = 'AirwayIM\\';
    if (!str_starts_with($class, $prefix)) {
        return;
    }
    $file = __DIR__ . '/' . str_replace('\\', '/', substr($class, strlen($prefix))) . '.php';
    if (is_file($file)) {
        require $file;
    }
});
