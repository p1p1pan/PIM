(async function bootLogPage() {
  await loadScriptsInOrder([
    "./common/shared.js",
    "./log/view.js",
    "./log/service.js",
    "./log/app.js",
  ]);
})();
