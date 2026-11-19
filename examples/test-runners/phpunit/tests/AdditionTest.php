<?php
// @facet: arithmetic/calculation/addition
// @facet-role: related
// @facet-proves: positive, negative
declare(strict_types=1);

use PHPUnit\Framework\TestCase;

require_once __DIR__ . '/../src/Arithmetic.php';

final class AdditionTest extends TestCase
{
    // Named mutation: replace addition with subtraction.
    public function testPositive(): void
    {
        self::assertSame(5, add(2, 3));
    }
    // Named mutation: replace addition with subtraction.
    public function testNegative(): void
    {
        self::assertSame(1, add(-2, 3));
    }
}
