// 登录页脚本入口：先加载共享能力，再加载登录业务逻辑。
(async function bootLoginPage() {
  await loadScriptsInOrder([
    "../common/shared.js",
    "../auth/login.js",
  ]);
})();
