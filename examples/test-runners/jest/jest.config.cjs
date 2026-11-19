module.exports = {
  testEnvironment: 'node',
  testMatch: ['<rootDir>/tests/addition.test.js', '<rootDir>/tests/multiplication.test.js'],
  reporters: ['default', ['jest-junit', {
    outputDirectory: 'reports',
    outputName: 'junit.xml',
    addFileAttribute: 'true',
  }]],
};
