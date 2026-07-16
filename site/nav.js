(function () {
  var nav = document.getElementById("site-nav");
  if (!nav) return;

  var toggle = nav.querySelector(".nav-toggle");
  var menu = document.getElementById("nav-menu");
  var backdrop = nav.querySelector(".nav-backdrop");
  var label = nav.querySelector(".nav-toggle-label");
  var scrollY = 0;

  function lockScroll() {
    scrollY = window.scrollY || window.pageYOffset || 0;
    document.body.style.position = "fixed";
    document.body.style.top = "-" + scrollY + "px";
    document.body.style.left = "0";
    document.body.style.right = "0";
    document.body.style.width = "100%";
  }

  function unlockScroll() {
    document.body.style.position = "";
    document.body.style.top = "";
    document.body.style.left = "";
    document.body.style.right = "";
    document.body.style.width = "";
    window.scrollTo(0, scrollY);
  }

  function setOpen(open) {
    var wasOpen = nav.classList.contains("nav-open");
    nav.classList.toggle("nav-open", open);
    document.body.classList.toggle("nav-open", open);
    if (toggle) toggle.setAttribute("aria-expanded", open ? "true" : "false");
    if (label) label.textContent = open ? "close" : "menu";
    if (toggle) toggle.setAttribute("aria-label", open ? "Close menu" : "Open menu");

    if (open && !wasOpen) {
      lockScroll();
    } else if (!open && wasOpen) {
      unlockScroll();
    }
  }

  function close() {
    setOpen(false);
  }

  if (toggle) {
    toggle.addEventListener("click", function () {
      setOpen(!nav.classList.contains("nav-open"));
    });
  }

  if (backdrop) {
    backdrop.addEventListener("click", close);
  }

  if (menu) {
    menu.querySelectorAll("a").forEach(function (link) {
      link.addEventListener("click", close);
    });
  }

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && nav.classList.contains("nav-open")) {
      close();
    }
  });
})();
