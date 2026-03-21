// 注册页初始化：校验输入 -> 调用注册接口 -> 跳转登录页面。
(function initRegister() {
  const form = document.getElementById("register-form");
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
    setStatus("注册中...", true);
    try {
      await apiRequest("/api/v1/register", {
        method: "POST",
        body: { username, password },
      });
      setStatus("注册成功，正在前往登录页...", true);
      setTimeout(() => location.replace("./login.html"), 400);
    } catch (err) {
      setStatus("注册失败: " + err.message, false);
    } finally {
      submitBtn.disabled = false;
    }
  });
})();
