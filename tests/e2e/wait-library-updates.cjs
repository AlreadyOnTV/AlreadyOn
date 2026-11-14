// Poll the durable queue through the authenticated fixture request context.
// Playwright waitForFunction treats an async predicate's Promise as truthy,
// so it can return after the first false result instead of polling again.
module.exports = async function waitLibraryUpdates(request, url, timeout = 120000) {
  const deadline = Date.now() + timeout;
  let state;
  while (Date.now() < deadline) {
    const response = await request.get(url + '/api/settings/folders/updates');
    if (!response.ok()) throw new Error(`Could not read library update state: ${response.status()}`);
    state = await response.json();
    if (state.state === 'failed') throw new Error(state.error);
    if (state.state === 'idle' && !state.needsScan && !state.needsMetadata && state.appliedRevision === state.revision) return state;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Library update did not finish: ${JSON.stringify(state)}`);
};
