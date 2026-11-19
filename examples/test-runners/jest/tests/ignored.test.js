// This would fail if the runner's explicit include pattern were ignored.
test('must stay excluded', () => { throw new Error('excluded fixture ran'); });
