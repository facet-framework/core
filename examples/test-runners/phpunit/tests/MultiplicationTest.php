<?php
// @facet: arithmetic/calculation/multiplication
// @facet-role: related
// @facet-proves: positive, zero
declare(strict_types=1);

use PHPUnit\Framework\TestCase;

require_once __DIR__ . '/../src/Arithmetic.php';

final class MultiplicationTest extends TestCase
{
    // Named mutation: replace multiplication with addition.
    public function testPositive(): void
    {
        self::assertSame(6, multiply(2, 3));
    }
    // Named mutation: replace multiplication with addition.
    public function testZero(): void
    {
        self::assertSame(0, multiply(0, 3));
    }
}
