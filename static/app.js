document.addEventListener("click", function (e) {
  var btn = e.target.closest("[data-copy]");
  if (!btn) return;
  var el = document.querySelector(btn.getAttribute("data-copy"));
  if (!el) return;
  var text = (el.textContent || "").trim();
  if (!navigator.clipboard) return;
  navigator.clipboard.writeText(text).then(function () {
    var prev = btn.textContent;
    btn.textContent = "Copied";
    setTimeout(function () { btn.textContent = prev; }, 1200);
  });
});
