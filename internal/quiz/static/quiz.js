// The Lord of the Rings Quiz: a small single-page app without dependencies
// or a build step, so it can be deleted with the rest of the quiz. Pages
// are routed by the URL's fragment, and built with el(), which never parses
// text as HTML, so names and questions can't inject markup.
(() => {
  "use strict";

  const app = document.getElementById("app");
  const nav = document.getElementById("nav");
  const joinedKey = "trail-tools.quiz.joined";

  // The signed in player, or null.
  let me = null;
  // The current page's render, to drop the results of a page left behind.
  let renderID = 0;
  // Functions that stop the current page's timers and prompts.
  let cleanups = [];
  // The open tab of the quiz master's page.
  let adminTab = "questions";

  const icons = {
    easy: `<svg viewBox="0 0 64 64"><path d="M2 54 Q32 6 62 54 Z" fill="#7fae4c"/><circle cx="32" cy="44" r="15" fill="#2f5a22" stroke="#d9b45a" stroke-width="3"/><path d="M32 29 V59 M17 44 H47" stroke="#24461a" stroke-width="1.5"/><circle cx="36.5" cy="45" r="2.6" fill="#e8b923"/><rect x="0" y="54" width="64" height="10" rx="4" fill="#3d6b26"/></svg>`,
    medium: `<svg viewBox="0 0 64 64"><path d="M12 60 V30 Q12 10 32 3 Q52 10 52 30 V60" fill="none" stroke="#f2f7fc" stroke-width="4"/><path d="M21 60 V32 Q21 17 32 12 Q43 17 43 32 V60" fill="rgba(255,255,255,.18)" stroke="#dbe7f3" stroke-width="2"/><path d="M27.5 24 V60 M32 19 V60 M36.5 24 V60" stroke="#cfe3f6" stroke-width="2" stroke-linecap="round"/></svg>`,
    hard: `<svg viewBox="0 0 64 64" class="eye"><path d="M3 32 Q32 4 61 32 Q32 60 3 32 Z" fill="#ff7a1a"/><path d="M10 32 Q32 12 54 32 Q32 52 10 32 Z" fill="#ffc04a"/><ellipse cx="32" cy="32" rx="3.6" ry="17" fill="#140302"/></svg>`,
    ring: `<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8" fill="none" stroke="#e8b923" stroke-width="3.5"/></svg>`,
  };

  const tiers = {
    easy: {
      blurb: "Questions any hobbit could answer between second breakfast and elevenses.",
      name: "The Shire",
      subtitle: "Fit for a hobbit",
    },
    medium: {
      blurb: "For those who have spent long evenings in Elrond's library.",
      name: "Rivendell",
      subtitle: "Counsel of the Elves",
    },
    hard: {
      blurb: "Lore so deep that even the Wise argue over it. Tread carefully.",
      name: "Mordor",
      subtitle: "One does not simply walk in",
    },
  };
  const tierOrder = ["easy", "medium", "hard"];

  // el creates an element with properties, event listeners (on*) and
  // children. Strings are added as text, never as HTML.
  function el(tag, props, ...children) {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(props ?? {})) {
      if (value === undefined || value === null || value === false) {
        continue;
      }
      if (key.startsWith("on")) {
        node.addEventListener(key.slice(2), value);
      } else if (key === "class") {
        node.className = value;
      } else if (key === "value" || key === "checked" || key === "disabled" || key === "hidden") {
        node[key] = value;
      } else {
        node.setAttribute(key, value === true ? "" : String(value));
      }
    }
    for (const child of children.flat(Infinity)) {
      if (child === undefined || child === null || child === false) {
        continue;
      }
      node.append(child instanceof Node ? child : String(child));
    }
    return node;
  }

  // fill replaces a node's children with nodes, which may be nested arrays
  // and include null for nodes left out, like el()'s children.
  function fill(parent, ...nodes) {
    parent.replaceChildren(
      ...nodes.flat(Infinity).filter((n) => n !== undefined && n !== null && n !== false),
    );
  }

  // icon returns one of the static icons above.
  function icon(name, className) {
    const holder = document.createElement("span");
    holder.innerHTML = icons[name];
    const svg = holder.firstElementChild;
    svg.classList.add(...className.split(" "));
    svg.setAttribute("aria-hidden", "true");
    return svg;
  }

  // sentence turns the server's lowercase messages into sentences.
  function sentence(message) {
    const s = String(message).trim();
    const capitalized = s.charAt(0).toUpperCase() + s.slice(1);
    return /[.!?]$/.test(capitalized) ? capitalized : `${capitalized}.`;
  }

  async function api(method, path, body) {
    const init = { credentials: "same-origin", headers: {}, method };
    if (method !== "GET") {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body ?? {});
    }
    const res = await fetch(`/quiz/api${path}`, init);
    let data = {};
    try {
      data = await res.json();
    } catch {
      // Errors without a body get a generic message below.
    }
    if (!res.ok) {
      if (res.status === 401 && me !== null) {
        me = null;
        renderNav();
      }
      throw new Error(sentence(data.error ?? `the request failed (${res.status}); try again`));
    }
    return data;
  }

  function toast(message) {
    const node = el("div", { class: "toast", role: "status" }, message);
    document.body.append(node);
    setTimeout(() => node.remove(), 2500);
  }

  function errorBox() {
    return el("div", { class: "error", hidden: true, role: "alert" });
  }

  function showError(box, error) {
    box.textContent = error instanceof Error ? error.message : String(error);
    box.hidden = false;
  }

  // busy disables a button and changes its label while the action runs.
  async function busy(button, label, action) {
    const original = button.textContent;
    button.disabled = true;
    button.textContent = label;
    try {
      return await action();
    } finally {
      button.disabled = false;
      button.textContent = original;
    }
  }

  function storageGet(key) {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  }

  function storageSet(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch {
      // Private windows may refuse storage, which is only a convenience.
    }
  }

  // Passkeys.

  const base64 = {
    decode: (s) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0)),
    encode: (buffer) => {
      let s = "";
      for (const b of new Uint8Array(buffer)) {
        s += String.fromCharCode(b);
      }
      return btoa(s);
    },
  };

  const passkeysSupported = () => typeof window.PublicKeyCredential !== "undefined";

  // isCancelled reports whether the player closed the passkey prompt, which
  // isn't worth an error message.
  const isCancelled = (error) =>
    error instanceof DOMException && (error.name === "NotAllowedError" || error.name === "AbortError");

  function passkeyError(error) {
    if (error instanceof DOMException && error.name === "InvalidStateError") {
      return new Error("This device already has a passkey for the quiz. Sign in with it instead.");
    }
    return error;
  }

  // The pending passkey autofill request of the sign in page.
  let conditional = null;

  function abortConditional() {
    if (conditional !== null) {
      conditional.abort();
      conditional = null;
    }
  }

  function signedIn(user) {
    me = user;
    storageSet(joinedKey, "1");
    toast(user.isAdmin ? `Welcome, ${user.name}. You are the quiz master!` : `Welcome, ${user.name}!`);
    location.hash = "#/";
    render();
  }

  async function register(name) {
    abortConditional();
    const begin = await api("POST", "/register/begin", { name });
    const credential = await navigator.credentials.create({
      publicKey: {
        attestation: "none",
        authenticatorSelection: { requireResidentKey: true, residentKey: "required", userVerification: "required" },
        challenge: base64.decode(begin.challenge),
        pubKeyCredParams: [
          { alg: -7, type: "public-key" },
          { alg: -8, type: "public-key" },
          { alg: -257, type: "public-key" },
        ],
        rp: { id: begin.rpId, name: "LOTR quiz on trailrunn.ing" },
        timeout: 120000,
        user: { displayName: begin.userName, id: base64.decode(begin.userHandle), name: begin.userName },
      },
    });
    const res = await api("POST", "/register/finish", {
      attestationObject: base64.encode(credential.response.attestationObject),
      challengeId: begin.challengeId,
      clientDataJson: base64.encode(credential.response.clientDataJSON),
    });
    return res.user;
  }

  // signIn asks for a passkey, either in a prompt or, with an AbortController,
  // in the name field's autofill.
  async function signIn(autofill) {
    const begin = await api("POST", "/login/begin");
    const options = {
      publicKey: {
        challenge: base64.decode(begin.challenge),
        rpId: begin.rpId,
        timeout: 120000,
        userVerification: "required",
      },
    };
    if (autofill !== undefined) {
      options.mediation = "conditional";
      options.signal = autofill.signal;
    }
    const credential = await navigator.credentials.get(options);
    const { response } = credential;
    const res = await api("POST", "/login/finish", {
      authenticatorData: base64.encode(response.authenticatorData),
      challengeId: begin.challengeId,
      clientDataJson: base64.encode(response.clientDataJSON),
      credentialId: base64.encode(credential.rawId),
      signature: base64.encode(response.signature),
      userHandle: response.userHandle === null ? "" : base64.encode(response.userHandle),
    });
    return res.user;
  }

  // Routing.

  function renderNav() {
    const hash = location.hash || "#/";
    const link = (href, label) =>
      el("a", { "aria-current": hash === href ? "page" : undefined, href }, label);
    fill(nav, 
      link("#/", "Quests"),
      link("#/leaderboard", "Leaderboard"),
      me?.isAdmin ? link("#/admin", "Quiz master") : null,
      me !== null
        ? el(
            "button",
            {
              onclick: async () => {
                await api("POST", "/logout").catch(() => {});
                me = null;
                location.hash = "#/";
                render();
              },
              type: "button",
            },
            "Sign out",
          )
        : null,
    );
  }

  async function render() {
    for (const cleanup of cleanups) {
      cleanup();
    }
    cleanups = [];
    abortConditional();
    renderNav();
    const id = ++renderID;
    // show replaces the page, unless the player has moved on to another.
    const show = (...nodes) => {
      if (id === renderID) {
        fill(app, nodes);
      }
      return id === renderID;
    };
    const hash = location.hash || "#/";
    const quiz = hash.match(/^#\/quiz\/(easy|medium|hard)$/);
    try {
      if (hash === "#/" || hash === "#") {
        await homePage(show);
      } else if (quiz !== null) {
        await quizPage(show, quiz[1]);
      } else if (hash === "#/leaderboard") {
        await leaderboardPage(show);
      } else if (hash === "#/admin") {
        await adminPage(show);
      } else {
        location.hash = "#/";
        return;
      }
    } catch (error) {
      show(
        el(
          "div",
          { class: "card center" },
          el("h2", {}, "The palantír clouded over"),
          el("p", {}, error instanceof Error ? error.message : String(error)),
          el("button", { class: "btn btn-primary", onclick: render, type: "button" }, "Try again"),
        ),
      );
    }
  }

  // Pages.

  async function homePage(show) {
    if (me === null) {
      signInPage(show);
      return;
    }
    show(el("p", { class: "loading" }, "Unrolling the map…"));
    const { quizzes } = await api("GET", "/quizzes");
    show(
      el(
        "div",
        { class: "hero" },
        el("h1", {}, `Well met, ${me.name}`),
        el(
          "p",
          {},
          me.isAdmin
            ? "You are the quiz master. Preview the quests here, and edit questions and players on the quiz master's page."
            : "Three quests lie before you. Take them in any order; harder quests are worth more points.",
        ),
      ),
      el(
        "div",
        { class: "tiers" },
        quizzes.map((q) => {
          const t = tiers[q.tier];
          let status = "Not yet attempted";
          if (q.submitted) {
            status = `Completed · ${q.correct} of ${q.questionCount} · ${q.correct * q.pointsPerQuestion} points`;
          } else if (me.isAdmin) {
            status = "Preview";
          }
          return el(
            "article",
            { class: `tier tier-${q.tier}` },
            icon(q.tier, "tier-icon"),
            el("h2", {}, t.name),
            el("p", { class: "subtitle" }, t.subtitle),
            el("p", {}, t.blurb),
            el(
              "p",
              { class: "meta" },
              `${q.questionCount} questions · ${q.pointsPerQuestion} ${q.pointsPerQuestion === 1 ? "point" : "points"} each`,
            ),
            el("span", { class: "status" }, status),
            el(
              "a",
              { class: "btn btn-primary", href: `#/quiz/${q.tier}` },
              q.submitted ? "See your answers" : me.isAdmin ? "Preview" : "Begin the quest",
            ),
          );
        }),
      ),
    );
  }

  function signInPage(show) {
    const joined = storageGet(joinedKey) !== null;
    const error = errorBox();
    const nameInput = el("input", {
      autocapitalize: "words",
      autocomplete: "username webauthn",
      enterkeyhint: "go",
      id: "name",
      maxlength: "40",
      placeholder: "e.g. Samwise the Brave",
      required: true,
      type: "text",
    });
    const joinButton = el(
      "button",
      { class: `btn ${joined ? "btn-secondary" : "btn-primary"} btn-block`, type: "submit" },
      "Join the Fellowship",
    );
    const signInButton = el(
      "button",
      { class: `btn ${joined ? "btn-primary" : "btn-secondary"} btn-block`, type: "button" },
      "Sign in with my passkey",
    );
    signInButton.addEventListener("click", async () => {
      error.hidden = true;
      abortConditional();
      try {
        const user = await busy(signInButton, "Waiting for your passkey…", () => signIn());
        signedIn(user);
      } catch (e) {
        if (!isCancelled(e)) {
          showError(error, passkeyError(e));
        }
        startAutofill();
      }
    });
    const form = el(
      "form",
      {
        onsubmit: async (event) => {
          event.preventDefault();
          error.hidden = true;
          try {
            const user = await busy(joinButton, "Forging your passkey…", () => register(nameInput.value));
            signedIn(user);
          } catch (e) {
            if (!isCancelled(e)) {
              showError(error, passkeyError(e));
            }
            startAutofill();
          }
        },
      },
      el(
        "div",
        { class: "field" },
        el("label", { for: "name" }, "Your name, as the leaderboard shall know it"),
        nameInput,
      ),
      joinButton,
    );
    const signInSection = el(
      "div",
      {},
      el("p", { class: "hint" }, joined ? "Welcome back! Your passkey remembers you." : "Joined before, on this or another device?"),
      signInButton,
    );
    const supported = passkeysSupported();
    show(
      el(
        "section",
        { class: "doors" },
        el("p", { class: "elvish" }, "Ennyn Durin Aran Moria · pedo mellon a minno"),
        el("h1", {}, "Speak, friend, and enter"),
        el(
          "p",
          {},
          "Join the quiz with a passkey: no passwords, just your fingerprint, face or PIN. Then take on the three quests and climb the leaderboard.",
        ),
        supported
          ? [
              joined ? signInSection : form,
              el("div", { class: "divider" }, joined ? "new to the quiz?" : "or"),
              joined ? form : signInSection,
            ]
          : el(
              "div",
              { class: "error" },
              "This browser doesn't support passkeys. Open the quiz in a recent browser, like Safari or Chrome on your phone.",
            ),
        error,
      ),
      el("p", { class: "center" }, el("a", { href: "#/leaderboard" }, "Peek at the leaderboard")),
    );
    // Offer the device's passkeys in the name field's autofill, so
    // returning players can sign in from there too.
    function startAutofill() {
      if (!supported || typeof PublicKeyCredential.isConditionalMediationAvailable !== "function") {
        return;
      }
      void PublicKeyCredential.isConditionalMediationAvailable().then((available) => {
        if (!available || !nameInput.isConnected) {
          return;
        }
        abortConditional();
        const controller = new AbortController();
        conditional = controller;
        signIn(controller)
          .then(signedIn)
          .catch((e) => {
            if (!isCancelled(e) && nameInput.isConnected) {
              showError(error, passkeyError(e));
            }
          });
      });
    }
    startAutofill();
  }

  // Answers in progress are kept for the browser tab, so reloading the page
  // or a phone discarding it doesn't lose them.
  function loadDraft(tier) {
    try {
      return JSON.parse(sessionStorage.getItem(`trail-tools.quiz.draft.${tier}`) ?? "{}");
    } catch {
      return {};
    }
  }

  function saveDraft(tier, draft) {
    try {
      if (draft === null) {
        sessionStorage.removeItem(`trail-tools.quiz.draft.${tier}`);
      } else {
        sessionStorage.setItem(`trail-tools.quiz.draft.${tier}`, JSON.stringify(draft));
      }
    } catch {
      // Storage is only a convenience.
    }
  }

  function banner(tier) {
    const t = tiers[tier];
    return el(
      "div",
      { class: `quiz-banner tier-${tier}` },
      icon(tier, "tier-icon"),
      el("div", {}, el("h1", {}, t.name), el("p", {}, t.subtitle)),
    );
  }

  async function quizPage(show, tier) {
    if (me === null) {
      location.hash = "#/";
      return;
    }
    show(el("p", { class: "loading" }, "Unrolling the map…"));
    const quiz = await api("GET", `/quizzes/${tier}`);
    if (quiz.submitted) {
      show(...results(quiz));
    } else {
      show(...questions(quiz));
    }
  }

  function questions(quiz) {
    const { tier } = quiz;
    if (quiz.questions.length === 0) {
      return [
        banner(tier),
        el(
          "div",
          { class: "card center" },
          el("p", {}, "The quiz master hasn't written the questions for this quest yet."),
          el("a", { class: "btn btn-secondary", href: "#/" }, "Back to the quests"),
        ),
      ];
    }
    const ids = new Set(quiz.questions.map((q) => q.id));
    const draft = Object.fromEntries(Object.entries(loadDraft(tier)).filter(([id]) => ids.has(id)));
    const error = errorBox();
    const progressText = el("div", { class: "progress-text" });
    const progressFill = el("div", { class: "progress-fill" });
    const submitButton = el("button", { class: "btn btn-primary", type: "button" }, "Submit");
    const total = quiz.questions.length;
    const update = () => {
      const answered = Object.keys(draft).length;
      progressText.textContent = me.isAdmin
        ? "Preview: the quiz master can't take the quiz."
        : `${answered} of ${total} answered`;
      progressFill.style.width = `${(answered / total) * 100}%`;
      submitButton.disabled = me.isAdmin || answered < total;
    };
    submitButton.addEventListener("click", async () => {
      if (!window.confirm("Submit your answers? You can't change them afterwards.")) {
        return;
      }
      error.hidden = true;
      try {
        const result = await busy(submitButton, "Submitting…", () =>
          api("POST", `/quizzes/${tier}/submit`, { answers: draft }),
        );
        saveDraft(tier, null);
        fill(app, results(result));
        window.scrollTo({ top: 0 });
      } catch (e) {
        showError(error, e);
      }
    });
    update();
    return [
      banner(tier),
      el("p", { class: "center" }, tiers[tier].blurb),
      quiz.questions.map((q, i) =>
        el(
          "fieldset",
          { class: "card question" },
          el("legend", {}, el("span", { class: "num" }, `${i + 1}.`), q.prompt),
          el(
            "div",
            { class: "choices" },
            q.choices.map((choice, j) =>
              el(
                "label",
                { class: "choice" },
                el("input", {
                  checked: draft[q.id] === j,
                  name: q.id,
                  onchange: () => {
                    draft[q.id] = j;
                    saveDraft(tier, draft);
                    update();
                  },
                  type: "radio",
                  value: String(j),
                }),
                el("span", {}, choice),
              ),
            ),
          ),
        ),
      ),
      error,
      el(
        "div",
        { class: "submit-bar" },
        el("div", { class: "progress" }, progressText, el("div", { class: "progress-track" }, progressFill)),
        submitButton,
      ),
    ];
  }

  function scoreQuote(fraction) {
    if (fraction === 1) {
      return "“Even the smallest person can change the course of the future.”";
    }
    if (fraction >= 0.7) {
      return "“You have my sword. And my bow. And my axe.”";
    }
    if (fraction >= 0.4) {
      return "“All we have to decide is what to do with the time that is given to us.”";
    }
    return "“Fool of a Took!”";
  }

  function results(quiz) {
    const { tier } = quiz;
    const total = quiz.questions.length;
    const correct = quiz.questions.filter((q) => q.correct).length;
    const revealed = quiz.questions.some((q) => q.answerIndex >= 0);
    return [
      banner(tier),
      el(
        "div",
        { class: "card score" },
        el("p", { class: "big" }, `${correct} / ${total}`),
        el(
          "p",
          {},
          `${correct * quiz.pointsPerQuestion} ${correct * quiz.pointsPerQuestion === 1 ? "point" : "points"} for the leaderboard`,
        ),
        el("p", { class: "quote muted" }, scoreQuote(total === 0 ? 0 : correct / total)),
        revealed ? null : el("p", { class: "hint" }, "The quiz master will reveal the correct answers later."),
        el(
          "div",
          { class: "btn-row" },
          el("a", { class: "btn btn-primary", href: "#/" }, "Back to the quests"),
          el("a", { class: "btn btn-secondary", href: "#/leaderboard" }, "See the leaderboard"),
        ),
      ),
      quiz.questions.map((q, i) =>
        el(
          "fieldset",
          { class: "card question" },
          el(
            "legend",
            {},
            el("span", { class: `result-badge ${q.correct ? "right" : "wrong"}` }, q.correct ? "Right" : "Wrong"),
            el("span", { class: "num" }, `${i + 1}.`),
            q.prompt,
          ),
          el(
            "div",
            { class: "choices" },
            q.choices.map((choice, j) => {
              let state = "";
              let mark = "";
              if (j === q.chosen) {
                state = q.correct ? "right" : "wrong";
                mark = "Your answer";
              } else if (j === q.answerIndex) {
                state = "right";
                mark = "Correct answer";
              }
              return el(
                "div",
                { class: `choice static ${state}` },
                el("span", {}, choice),
                mark === "" ? null : el("span", { class: "mark" }, mark),
              );
            }),
          ),
        ),
      ),
    ];
  }

  // title is a player's title by the share of the points they have.
  function title(fraction) {
    if (fraction >= 0.9) {
      return "Istar of the West";
    }
    if (fraction >= 0.7) {
      return "Loremaster of Rivendell";
    }
    if (fraction >= 0.5) {
      return "Ranger of the North";
    }
    if (fraction >= 0.3) {
      return "Hobbit of the Shire";
    }
    return "Lost in Moria";
  }

  async function leaderboardPage(show) {
    show(el("p", { class: "loading" }, "Counting the spoils…"));
    const card = el("div", { class: "card" });
    const load = async () => {
      const board = await api("GET", "/leaderboard");
      fill(card, leaderboard(board));
    };
    await load();
    show(
      el(
        "div",
        { class: "hero" },
        el("h1", {}, "The Hall of Fire"),
        el("p", {}, "Shire questions are worth 1 point, Rivendell 2 and Mordor 3. Ties go to whoever finished first."),
      ),
      card,
    );
    // Keep the leaderboard fresh, e.g. on a screen at the party.
    const timer = setInterval(() => void load().catch(() => {}), 10000);
    cleanups.push(() => clearInterval(timer));
  }

  function leaderboard(board) {
    if (board.entries.length === 0) {
      return [el("p", { class: "center muted" }, "No one has finished a quest yet. Be the first!")];
    }
    const tierCell = (entry, tier) => {
      const r = entry.tiers[tier];
      return r.submitted ? `${r.correct}/${r.total}` : "–";
    };
    return [
      el(
        "div",
        { class: "table-wrap" },
        el(
          "table",
          {},
          el(
            "thead",
            {},
            el(
              "tr",
              {},
              el("th", { scope: "col" }, "#"),
              el("th", { scope: "col" }, "Player"),
              tierOrder.map((t) =>
                el("th", { class: "num tier-col", scope: "col" }, el("span", { class: `dot dot-${t}` }), tiers[t].name),
              ),
              el("th", { class: "num", scope: "col" }, "Points"),
            ),
          ),
          el(
            "tbody",
            {},
            board.entries.map((entry) =>
              el(
                "tr",
                { class: `${entry.userId === me?.id ? "me" : ""} podium-${entry.rank}` },
                el("td", { class: "rank" }, entry.rank === 1 ? icon("ring", "tier-icon-inline") : entry.rank),
                el(
                  "td",
                  {},
                  entry.name,
                  el("span", { class: "title" }, title(board.maxPoints === 0 ? 0 : entry.points / board.maxPoints)),
                  el(
                    "span",
                    { class: "mini-tiers" },
                    tierOrder.map((t) => el("span", {}, el("span", { class: `dot dot-${t}` }), tierCell(entry, t))),
                  ),
                ),
                tierOrder.map((t) => el("td", { class: "num tier-col" }, tierCell(entry, t))),
                el("td", { class: "num points" }, entry.points),
              ),
            ),
          ),
        ),
      ),
      el("p", { class: "hint center" }, `Out of ${board.maxPoints} possible points.`),
    ];
  }

  // The quiz master's page.

  async function adminPage(show) {
    if (!me?.isAdmin) {
      show(el("div", { class: "card center" }, el("p", {}, "Only the quiz master may enter here.")));
      return;
    }
    const content = el("div", {});
    const tabs = [
      ["questions", "Questions"],
      ["players", "Players"],
      ["settings", "Settings"],
    ];
    const tabBar = el("div", { class: "tabs", role: "tablist" });
    const renderTabs = () =>
      fill(
        tabBar,
        tabs.map(([key, label]) =>
          el(
            "button",
            {
              "aria-selected": String(adminTab === key),
              onclick: () => {
                adminTab = key;
                renderTabs();
                void loadTab();
              },
              role: "tab",
              type: "button",
            },
            label,
          ),
        ),
      );
    const loadTab = async () => {
      fill(content, el("p", { class: "loading" }, "Opening the archives…"));
      try {
        const reload = () => void loadTab();
        if (adminTab === "questions") {
          fill(content, await questionsTab(reload));
        } else if (adminTab === "players") {
          fill(content, await playersTab(reload));
        } else {
          fill(content, await settingsTab());
        }
      } catch (e) {
        const error = errorBox();
        showError(error, e);
        fill(content, error);
      }
    };
    renderTabs();
    show(el("div", { class: "hero" }, el("h1", {}, "The quiz master's study")), tabBar, content);
    await loadTab();
  }

  async function questionsTab(reload) {
    const { questions } = await api("GET", "/admin/questions");
    const byTier = Object.fromEntries(tierOrder.map((t) => [t, questions.filter((q) => q.tier === t)]));
    const move = async (list, index, delta) => {
      const reordered = [...list];
      const [q] = reordered.splice(index, 1);
      reordered.splice(index + delta, 0, q);
      await Promise.all(
        reordered.map((item, position) =>
          item.position === position ? null : api("PUT", `/admin/questions/${item.id}`, { ...item, position }),
        ),
      );
      reload();
    };
    return [
      el(
        "p",
        { class: "hint" },
        "Changing a question's correct answer re-scores everyone who answered it. Reordering its choices after players have answered changes which choice their answers point to.",
      ),
      tierOrder.map((tier) => {
        const list = byTier[tier];
        const section = el("section", {});
        const addButton = el(
          "button",
          {
            class: "btn btn-secondary",
            onclick: () => {
              addButton.replaceWith(questionForm({ answerIndex: 0, choices: ["", "", "", ""], prompt: "", tier }, reload));
            },
            type: "button",
          },
          "Add a question",
        );
        section.append(
          el(
            "h2",
            { class: "section-title" },
            icon(tier, "tier-icon-inline"),
            `${tiers[tier].name} · ${list.length}`,
          ),
          ...list.map((q, i) => {
            const card = el(
              "div",
              { class: "card admin-question" },
              el("div", { class: "prompt" }, `${i + 1}. ${q.prompt}`),
              el(
                "ol",
                {},
                q.choices.map((c, j) =>
                  el("li", { class: j === q.answerIndex ? "correct" : "" }, c, j === q.answerIndex ? " ✓" : ""),
                ),
              ),
              el(
                "div",
                { class: "btn-row" },
                el(
                  "button",
                  {
                    class: "btn btn-primary btn-small",
                    onclick: () => card.replaceWith(questionForm(q, reload)),
                    type: "button",
                  },
                  "Edit",
                ),
                el(
                  "button",
                  {
                    "aria-label": "Move up",
                    class: "icon-btn",
                    disabled: i === 0,
                    onclick: () => void move(list, i, -1),
                    title: "Move up",
                    type: "button",
                  },
                  "↑",
                ),
                el(
                  "button",
                  {
                    "aria-label": "Move down",
                    class: "icon-btn",
                    disabled: i === list.length - 1,
                    onclick: () => void move(list, i, 1),
                    title: "Move down",
                    type: "button",
                  },
                  "↓",
                ),
              ),
            );
            return card;
          }),
          addButton,
        );
        return section;
      }),
    ];
  }

  let formCount = 0;

  // questionForm edits a question, or creates one if it has no ID.
  function questionForm(question, reload) {
    const formID = ++formCount;
    const error = errorBox();
    let answerIndex = question.answerIndex;
    const tierSelect = el(
      "select",
      { id: `tier-${formID}` },
      tierOrder.map((t) => el("option", { selected: t === question.tier, value: t }, tiers[t].name)),
    );
    tierSelect.value = question.tier;
    const prompt = el("textarea", { id: `prompt-${formID}`, maxlength: "500", value: question.prompt });
    const choicesBox = el("div", {});
    let choices = [...question.choices];
    const readChoices = () => {
      choices = [...choicesBox.querySelectorAll("input[type=text]")].map((input) => input.value);
    };
    const renderChoices = () => {
      fill(
        choicesBox,
        choices.map((c, j) =>
          el(
            "div",
            { class: "choice-edit" },
            el("input", {
              "aria-label": `Choice ${j + 1} is correct`,
              checked: j === answerIndex,
              name: `correct-${formID}`,
              onchange: () => {
                answerIndex = j;
              },
              title: "Correct answer",
              type: "radio",
            }),
            el("input", { "aria-label": `Choice ${j + 1}`, maxlength: "200", type: "text", value: c }),
            el(
              "button",
              {
                "aria-label": `Remove choice ${j + 1}`,
                class: "icon-btn",
                disabled: choices.length <= 2,
                onclick: () => {
                  readChoices();
                  choices.splice(j, 1);
                  if (answerIndex === j) {
                    answerIndex = 0;
                  } else if (answerIndex > j) {
                    answerIndex--;
                  }
                  renderChoices();
                },
                title: "Remove choice",
                type: "button",
              },
              "✕",
            ),
          ),
        ),
        el(
          "button",
          {
            class: "btn btn-secondary btn-small",
            disabled: choices.length >= 6,
            onclick: () => {
              readChoices();
              choices.push("");
              renderChoices();
            },
            type: "button",
          },
          "Add a choice",
        ),
      );
    };
    renderChoices();
    const saveButton = el("button", { class: "btn btn-primary", type: "submit" }, "Save");
    const deleteButton =
      question.id === undefined
        ? null
        : el(
            "button",
            {
              class: "btn btn-danger",
              onclick: async () => {
                if (!window.confirm("Delete this question, and everyone's answers to it?")) {
                  return;
                }
                try {
                  await busy(deleteButton, "Deleting…", () => api("DELETE", `/admin/questions/${question.id}`));
                  toast("Question deleted");
                  reload();
                } catch (e) {
                  showError(error, e);
                }
              },
              type: "button",
            },
            "Delete",
          );
    return el(
      "form",
      {
        class: "card",
        onsubmit: async (event) => {
          event.preventDefault();
          readChoices();
          error.hidden = true;
          const body = {
            answerIndex,
            choices,
            position: question.position ?? 0,
            prompt: prompt.value,
            tier: tierSelect.value,
          };
          try {
            await busy(saveButton, "Saving…", () =>
              question.id === undefined
                ? api("POST", "/admin/questions", body)
                : api("PUT", `/admin/questions/${question.id}`, body),
            );
            toast("Question saved");
            reload();
          } catch (e) {
            showError(error, e);
          }
        },
      },
      el("h3", {}, question.id === undefined ? "New question" : "Edit question"),
      el("div", { class: "field" }, el("label", { for: `tier-${formID}` }, "Quest"), tierSelect),
      el("div", { class: "field" }, el("label", { for: `prompt-${formID}` }, "Question"), prompt),
      el(
        "div",
        { class: "field" },
        el("label", {}, "Choices"),
        el("p", { class: "hint" }, "Select the correct answer with the button on its left."),
        choicesBox,
      ),
      error,
      el(
        "div",
        { class: "btn-row" },
        saveButton,
        el("button", { class: "btn btn-secondary", onclick: reload, type: "button" }, "Cancel"),
        deleteButton,
      ),
    );
  }

  async function playersTab(reload) {
    const { users } = await api("GET", "/admin/users");
    const error = errorBox();
    const act = async (button, label, action, message) => {
      error.hidden = true;
      try {
        await busy(button, label, action);
        toast(message);
        reload();
      } catch (e) {
        showError(error, e);
      }
    };
    return [
      el("p", { class: "hint" }, `${users.length} ${users.length === 1 ? "player has" : "players have"} joined.`),
      error,
      users.map((u) => {
        const isMe = u.id === me.id;
        const update = (changes) => api("PUT", `/admin/users/${u.id}`, { isAdmin: u.isAdmin, name: u.name, ...changes });
        const renameButton = el("button", { class: "btn btn-secondary btn-small", type: "button" }, "Rename");
        renameButton.addEventListener("click", () => {
          const name = window.prompt(`New name for ${u.name}:`, u.name);
          if (name !== null && name.trim() !== "" && name !== u.name) {
            void act(renameButton, "Renaming…", () => update({ name }), "Player renamed");
          }
        });
        const adminButton = isMe
          ? null
          : el(
              "button",
              { class: "btn btn-secondary btn-small", type: "button" },
              u.isAdmin ? "Revoke quiz master" : "Make quiz master",
            );
        adminButton?.addEventListener("click", () => {
          void act(adminButton, "Saving…", () => update({ isAdmin: !u.isAdmin }), "Player updated");
        });
        const removeButton = isMe
          ? null
          : el("button", { class: "btn btn-danger btn-small", type: "button" }, "Remove");
        removeButton?.addEventListener("click", () => {
          if (window.confirm(`Remove ${u.name} and all their answers? They can join again with a new passkey.`)) {
            void act(removeButton, "Removing…", () => api("DELETE", `/admin/users/${u.id}`), "Player removed");
          }
        });
        const resets = u.submitted.map((t) => {
          const button = el("button", { class: "btn btn-secondary btn-small", type: "button" }, `Reset ${tiers[t].name}`);
          button.addEventListener("click", () => {
            if (window.confirm(`Reset ${u.name}'s ${tiers[t].name} quest, so they can take it again?`)) {
              void act(button, "Resetting…", () => api("DELETE", `/admin/users/${u.id}/submissions/${t}`), "Quest reset");
            }
          });
          return button;
        });
        return el(
          "div",
          { class: "card player" },
          el(
            "div",
            {},
            el(
              "div",
              { class: "player-name" },
              u.name,
              u.isAdmin ? el("span", { class: "badge" }, "Quiz master") : null,
              isMe ? el("span", { class: "muted" }, " (you)") : null,
            ),
            el(
              "div",
              { class: "muted" },
              `Joined ${new Date(u.createTime).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`,
              u.submitted.length === 0
                ? " · No quests submitted"
                : ` · Submitted ${u.submitted.map((t) => tiers[t].name).join(", ")}`,
            ),
          ),
          el("div", { class: "btn-row" }, renameButton, adminButton, resets, removeButton),
        );
      }),
    ];
  }

  async function settingsTab() {
    const { revealAnswers } = await api("GET", "/me");
    const error = errorBox();
    const toggle = el("input", {
      checked: revealAnswers,
      id: "reveal",
      onchange: async () => {
        error.hidden = true;
        toggle.disabled = true;
        try {
          await api("PUT", "/admin/settings", { revealAnswers: toggle.checked });
          toast(toggle.checked ? "Answers revealed" : "Answers hidden");
        } catch (e) {
          toggle.checked = !toggle.checked;
          showError(error, e);
        } finally {
          toggle.disabled = false;
        }
      },
      type: "checkbox",
    });
    const link = `${location.origin}/quiz`;
    return [
      el(
        "div",
        { class: "card" },
        el(
          "label",
          { class: "toggle", for: "reveal" },
          toggle,
          el(
            "span",
            {},
            "Reveal the correct answers",
            el(
              "span",
              { class: "hint" },
              el("br", {}),
              "Players who submitted a quest see which answers were correct. Leave it off until everyone has finished.",
            ),
          ),
        ),
        error,
      ),
      el(
        "div",
        { class: "card" },
        el("h3", {}, "Invite the Fellowship"),
        el("p", {}, "Guests join at:"),
        el("p", { class: "points" }, el("a", { href: link }, link)),
        el(
          "p",
          { class: "hint" },
          "Show the leaderboard on a big screen; it refreshes by itself every 10 seconds.",
        ),
      ),
    ];
  }

  async function start() {
    window.addEventListener("hashchange", render);
    try {
      const res = await api("GET", "/me");
      me = res.user;
    } catch {
      // The page shows the error when it loads its data.
    }
    await render();
  }

  void start();
})();
