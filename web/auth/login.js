// 登录页初始化：校验输入 -> 调用登录接口 -> 保存会话 -> 跳转 IM 页面。
(function initLogin() {
  const auth = loadAuth();
  if (auth.token) {
    location.replace("./im.html");
    return;
  }

  const form = document.getElementById("login-form");
  const statusEl = document.getElementById("status");
  const submitBtn = document.getElementById("submit-btn");

  function setStatus(msg, ok) {
    statusEl.textContent = msg || "";
    statusEl.className = "status " + (ok ? "ok" : "err");
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(form);
    const username = String(fd.get("username") || "").trim();
    const password = String(fd.get("password") || "").trim();
    if (!username || !password) {
      setStatus("请输入用户名和密码", false);
      return;
    }
    submitBtn.disabled = true;
    setStatus("登录中...", true);
    try {
      const res = await apiRequest("/api/v1/login", {
        method: "POST",
        body: { username, password },
      });
      saveAuth(res.token, res.user);
      setStatus("登录成功，正在跳转...", true);
      setTimeout(() => location.replace("./im.html"), 250);
    } catch (err) {
      setStatus("登录失败: " + err.message, false);
    } finally {
      submitBtn.disabled = false;
    }
  });
})();
