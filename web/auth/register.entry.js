// 注册页脚本入口：先加载共享能力，再加载注册业务逻辑。
(async function bootRegisterPage() {
  await loadScriptsInOrder([
    "./common/shared.js",
    "./auth/register.js",
  ]);
})();
