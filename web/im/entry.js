// IM 页面脚本装配入口：先共享工具，再模板/方法，最后挂载应用。
(async function bootIMPage() {
  await loadScriptsInOrder([
    "../common/shared.js",
    "../im/view.js",
    "../im/service.js",
    "../im/app.js",
  ]);
})();
