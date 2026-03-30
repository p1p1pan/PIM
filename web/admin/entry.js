(async function bootAdminPage() {
  await loadScriptsInOrder([
    "../common/shared.js",
    "../admin/view.js",
    "../admin/service.js",
    "../admin/app.js",
  ]);
})();
