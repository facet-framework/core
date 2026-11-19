import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['tests/addition.test.js', 'tests/multiplication.test.js'],
    reporters: ['default', ['junit', { addFileAttribute: true }]],
    outputFile: { junit: 'reports/junit.xml' },
  },
});
