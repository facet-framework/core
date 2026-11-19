// @facet: arithmetic/calculation/addition
// @facet-role: related
// @facet-proves: positive, negative
const { add } = require('../src/arithmetic');

// Named mutation: replace addition with subtraction.
test('positive', () => {
  expect(add(2, 3)).toBe(5);
});
// Named mutation: replace addition with subtraction.
test('negative', () => {
  expect(add(-2, 3)).toBe(1);
});
