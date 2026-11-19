// @facet: arithmetic/calculation/multiplication
// @facet-role: related
// @facet-proves: positive, zero
import { test, expect } from 'vitest';
import { multiply } from '../src/arithmetic.js';

// Named mutation: replace multiplication with addition.
test('positive', () => {
  expect(multiply(2, 3)).toBe(6);
});
// Named mutation: replace multiplication with addition.
test('zero', () => {
  expect(multiply(0, 3)).toBe(0);
});
