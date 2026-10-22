// IM 页面脚本装配入口：先共享工具，再模板/方法，最后挂载应用。
// ?v= 用于打破浏览器对静态 JS 的强缓存（改前端后若看不到效果，先硬刷新；此版本号也会强制换新）。
(async function bootIMPage() {
  const v = "20261022p";
  await loadScriptsInOrder([
    `../common/shared.js?v=${v}`,
    `../im/view.js?v=${v}`,
    `../im/service.js?v=${v}`,
    `../im/app.js?v=${v}`,
  ]);
})();
