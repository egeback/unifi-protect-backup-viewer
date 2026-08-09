document.getElementById("login-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const form = e.target;
  const errorEl = document.getElementById("login-error");
  errorEl.hidden = true;

  const body = JSON.stringify({
    username: form.username.value,
    password: form.password.value,
  });

  const res = await fetch("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
  });

  if (res.ok) {
    window.location.href = "/";
  } else {
    errorEl.hidden = false;
  }
});
