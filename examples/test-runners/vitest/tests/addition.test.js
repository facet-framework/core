// @facet: arithmetic/calculation/addition
// @facet-role: related
// @facet-proves: positive, negative
import { test, expect } from 'vitest';
import { add } from '../src/arithmetic.js';

// Named mutation: replace addition with subtraction.
test('positive', () => {
  expect(add(2, 3)).toBe(5);
});
// Named mutation: replace addition with subtraction.
test('negative', () => {
  expect(add(-2, 3)).toBe(1);
});
