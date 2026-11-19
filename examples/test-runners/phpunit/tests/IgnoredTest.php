<?php
use PHPUnit\Framework\TestCase;
final class IgnoredTest extends TestCase
{
    public function testExcluded(): void { self::fail('excluded fixture ran'); }
}
