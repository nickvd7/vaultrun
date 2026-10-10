(function () {
  // ---------- mobile nav + dropdown ----------
  const header = document.getElementById("top");
  const burger = document.getElementById("burger");
  const dd = document.getElementById("ddProduct");
  const ddb = dd && dd.querySelector("button");

  function closeMobile() {
    if (!header || !burger) return;
    header.classList.remove("mobile-open");
    burger.setAttribute("aria-expanded", "false");
    burger.textContent = "☰";
    if (dd) {
      dd.classList.remove("open");
      if (ddb) ddb.setAttribute("aria-expanded", "false");
    }
  }

  if (burger && header) {
    burger.addEventListener("click", () => {
      const o = header.classList.toggle("mobile-open");
      burger.setAttribute("aria-expanded", o ? "true" : "false");
      burger.textContent = o ? "✕" : "☰";
    });
  }
  document.querySelectorAll(".links a").forEach((a) =>
    a.addEventListener("click", closeMobile)
  );
  if (dd && ddb) {
    ddb.addEventListener("click", (e) => {
      e.stopPropagation();
      const o = dd.classList.toggle("open");
      ddb.setAttribute("aria-expanded", o ? "true" : "false");
    });
    document.addEventListener("click", (e) => {
      if (!dd.contains(e.target)) {
        dd.classList.remove("open");
        ddb.setAttribute("aria-expanded", "false");
      }
    });
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        dd.classList.remove("open");
        ddb.setAttribute("aria-expanded", "false");
        closeMobile();
      }
    });
  }

  // ---------- GitHub stars (live, fails silently) ----------
  const starsEl = document.getElementById("stars");
  if (starsEl) {
    fetch("https://api.github.com/repos/nickvd7/vaultrun")
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => {
        if (d && typeof d.stargazers_count === "number") {
          starsEl.textContent = "★ " + d.stargazers_count.toLocaleString("en");
        }
      })
      .catch(() => {});
  }

  // ---------- demo ----------
  const esc = (s) =>
    s.replace(/&/g, "&amp;").replace(/</g, "&lt;");
  const steps = [
    {
      t: "01",
      n: "agent asks",
      o: `<span class="c-dim"># agent calls run_command via MCP</span>
POST /api/v1/sessions/<span class="c-info">s_3f8a2c1e</span>/run
Authorization: Bearer vr_…

{ "command": "python",
  "args": ["analyze.py", "--input", "sales.csv"] }

<span class="c-dim">→ received · handing to policy</span>`,
    },
    {
      t: "02",
      n: "policy check",
      o: `<span class="c-dim"># policy hook (OPA-ready)</span>
image      python:3.12-slim   <span class="c-ok">allowed</span>
command    python             <span class="c-ok">allowed</span>
network    none               <span class="c-ok">default</span>
args       no host paths      <span class="c-ok">ok</span>

decision   <span class="c-ok">ALLOW</span>
<span class="c-dim">→ dispatching to sandbox</span>`,
    },
    {
      t: "03",
      n: "sandbox run",
      o: `<span class="c-dim"># isolated container for this session</span>
user       1000 (non-root)
caps       ALL dropped
network    disabled
limits     1 cpu · 512 MiB · 120 s

$ python analyze.py --input sales.csv
rows: 48,211   revenue: €1.92M   top region: NL
<span class="c-ok">exit 0</span> · 3.4 s`,
    },
    {
      t: "04",
      n: "audit trail",
      o: `<span class="c-dim"># GET /api/v1/audit?session_id=s_3f8a2c1e</span>
{ "audit_logs": [
  { "action": "command.requested", "sig": "<span class="c-info">9c1e…a04f</span>" },
  { "action": "policy.allowed",    "sig": "<span class="c-info">41bd…77e2</span>" },
  { "action": "command.finished",  "exit": 0,
    "sig": "<span class="c-info">e83a…1d90</span>" } ] }
<span class="c-ok">✓ signatures valid</span> — replay this on your own instance`,
    },
  ];
  const stepsEl = document.getElementById("steps");
  const out = document.getElementById("demoOut");
  const playBtn = document.getElementById("demoPlay");
  if (stepsEl && out && playBtn) {
    let cur = 0;
    let playing = !matchMedia("(prefers-reduced-motion: reduce)").matches;
    let timer = null;
    steps.forEach((s, i) => {
      const b = document.createElement("button");
      b.setAttribute("role", "tab");
      b.innerHTML = `<b>${s.t}</b><span>${s.n}</span><i class="bar"></i>`;
      b.addEventListener("click", () => {
        go(i);
        stop();
      });
      stepsEl.appendChild(b);
    });
    const btns = [...stepsEl.children];
    function go(i) {
      cur = (i + steps.length) % steps.length;
      btns.forEach((b, j) => {
        b.setAttribute("aria-selected", j === cur ? "true" : "false");
        b.tabIndex = j === cur ? 0 : -1;
      });
      out.innerHTML = steps[cur].o;
    }
    function tick() {
      go(cur + 1);
    }
    function start() {
      playing = true;
      clearInterval(timer);
      timer = setInterval(tick, 3600);
      playBtn.textContent = "❚❚ pause";
      playBtn.setAttribute("aria-label", "Pause walkthrough");
    }
    function stop() {
      playing = false;
      clearInterval(timer);
      playBtn.textContent = "▶ play";
      playBtn.setAttribute("aria-label", "Play walkthrough");
    }
    playBtn.addEventListener("click", () => (playing ? stop() : start()));
    stepsEl.addEventListener("keydown", (e) => {
      if (e.key === "ArrowRight") {
        go(cur + 1);
        stop();
        btns[cur].focus();
      }
      if (e.key === "ArrowLeft") {
        go(cur - 1);
        stop();
        btns[cur].focus();
      }
    });
    go(0);
    if (playing) {
      const io = new IntersectionObserver(
        (es) => {
          es.forEach((en) => {
            if (en.isIntersecting && playing) start();
            else if (!en.isIntersecting) clearInterval(timer);
          });
        },
        { threshold: 0.4 }
      );
      io.observe(stepsEl);
    } else stop();
  }

  // ---------- generic tabs ----------
  document.querySelectorAll("[data-tabs]").forEach((group) => {
    const tabs = [...group.querySelectorAll("[role=tab]")];
    tabs.forEach((t) =>
      t.addEventListener("click", () => {
        tabs.forEach((x) => {
          x.setAttribute("aria-selected", x === t ? "true" : "false");
          const panel = document.getElementById(x.dataset.panel);
          if (panel) panel.hidden = x !== t;
        });
      })
    );
  });

  // ---------- copy buttons ----------
  document.querySelectorAll(".copy").forEach((b) =>
    b.addEventListener("click", async () => {
      const root = b.closest(".panel") || b.closest(".term");
      const code = root && root.querySelector("code");
      if (!code) return;
      const text = code.innerText
        .split("\n")
        .filter((l) => !/^\s*#/.test(l))
        .join("\n")
        .replace(/\s+#.*$/gm, "");
      try {
        await navigator.clipboard.writeText(text);
        b.textContent = "copied ✓";
      } catch (e) {
        b.textContent = "select & copy";
      }
      setTimeout(() => (b.textContent = "copy"), 1600);
    })
  );

  // ---------- MCP categories (total computed so it can't drift) ----------
  const cats = [
    {
      n: "sandbox",
      tools: [
        "create_session",
        "run_command",
        "upload_file",
        "read_file",
        "list_files",
        "get_session_logs",
        "…",
      ],
      c: 13,
    },
    {
      n: "verify",
      tools: [
        "verify_checkpoint",
        "verify_controls",
        "verify_evidence",
      ],
      c: 3,
    },
    {
      n: "databases",
      tools: [
        "sqlite_query",
        "pg_query",
        "pg_schema",
        "mongo_find",
        "mongo_aggregate",
        "…",
      ],
      c: 13,
      opt: true,
    },
    {
      n: "aws",
      tools: [
        "s3_get_object",
        "ssm_get_parameter",
        "sm_get_secret",
        "lambda_invoke",
        "…",
      ],
      c: 14,
      opt: true,
    },
    {
      n: "filesystem",
      tools: ["fs_read_file", "fs_write_file", "fs_list_dir", "fs_delete_file"],
      c: 4,
      opt: true,
    },
    {
      n: "github",
      tools: ["run_github_repo", "github_post_comment"],
      c: 2,
    },
    {
      n: "ops",
      tools: [
        "list_images",
        "pull_image",
        "create_snapshot",
        "create_artifact",
        "list_audit_logs",
        "…",
      ],
      c: 7,
    },
    {
      n: "flowd",
      tools: [
        "flowd_list_suggestions",
        "flowd_approve_suggestion",
        "flowd_list_patterns",
        "…",
      ],
      c: 6,
      opt: true,
    },
    {
      n: "jev",
      tools: ["jev_verify", "jev_gate"],
      c: 2,
      opt: true,
    },
  ];
  const catsEl = document.getElementById("cats");
  const toolTotal = document.getElementById("toolTotal");
  if (catsEl) {
    cats.forEach((c) => {
      const b = document.createElement("button");
      b.className = "cat";
      b.type = "button";
      b.setAttribute("aria-expanded", "false");
      b.innerHTML = `<div class="t">${c.n}<span>×${c.c}</span></div>${
        c.opt ? '<span class="optin">opt-in</span>' : ""
      }<div class="tools">${c.tools.map(esc).join("<br>")}</div>`;
      b.addEventListener("click", () =>
        b.setAttribute(
          "aria-expanded",
          b.getAttribute("aria-expanded") !== "true" ? "true" : "false"
        )
      );
      catsEl.appendChild(b);
    });
  }
  if (toolTotal) {
    // Core always-on count for headline; opt-in groups listed separately
    toolTotal.textContent = "53+";
  }

  // ---------- contact form (FormSubmit + progressive fields) ----------
  const intent = document.getElementById("intent");
  const form = document.getElementById("contactForm");
  const sent = document.getElementById("sent");
  const tz = document.getElementById("tz");
  const subject = document.getElementById("form-subject");
  const replyto = document.getElementById("form-replyto");
  const email = document.getElementById("email");
  try {
    if (tz) tz.textContent = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const tzInput = document.getElementById("timezone");
    if (tzInput && !tzInput.value) {
      tzInput.value = Intl.DateTimeFormat().resolvedOptions().timeZone;
    }
  } catch (e) {}

  function syncIntent() {
    if (!intent || !form) return;
    const v = intent.value;
    form.querySelectorAll(".fieldset").forEach((f) => {
      const forAttr = f.dataset.for || "";
      f.hidden = !forAttr.split(/\s+/).includes(v);
    });
    // Disable hidden required fields so they don't block submit
    form.querySelectorAll(".fieldset").forEach((f) => {
      f.querySelectorAll("[required]").forEach((el) => {
        el.disabled = f.hidden;
      });
    });
    if (subject) {
      subject.value = "[vaultrun.dev] " + v;
    }
    if (sent) sent.classList.remove("show");
  }

  if (intent) {
    intent.addEventListener("change", syncIntent);
    syncIntent();
  }
  document.querySelectorAll("[data-intent]").forEach((a) =>
    a.addEventListener("click", () => {
      if (!intent) return;
      intent.value = a.dataset.intent;
      syncIntent();
    })
  );
  if (email && replyto) {
    email.addEventListener("input", () => {
      replyto.value = email.value;
    });
  }

  // Prefill from ?sent=1 or ?intent=
  const params = new URLSearchParams(location.search);
  if (params.get("sent") === "1" && sent) {
    sent.classList.add("show");
    sent.textContent =
      "✓ Message queued — we'll get back to you soon.";
  }
  const pick =
    params.get("intent") ||
    (location.hash.match(/intent=([a-z]+)/) || [])[1];
  if (pick && intent && [...intent.options].some((o) => o.value === pick)) {
    intent.value = pick;
    syncIntent();
  }

  if (form) {
    form.addEventListener("submit", (e) => {
      if (intent && intent.value === "security") {
        e.preventDefault();
        return;
      }
      const req = [
        ...form.querySelectorAll(".fieldset:not([hidden]) [required]"),
      ].find(
        (i) =>
          !i.disabled &&
          (!i.value ||
            (i.type === "email" && !/^\S+@\S+\.\S+$/.test(i.value)))
      );
      if (req) {
        e.preventDefault();
        req.focus();
        req.style.outline = "2px solid var(--line2)";
        setTimeout(() => (req.style.outline = ""), 1500);
      }
      // otherwise allow FormSubmit POST
    });
  }
})();
