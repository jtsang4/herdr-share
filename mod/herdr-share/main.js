// herdr-share: share the focused terminal session through your relay.
//
// Opened as a dialog (`mod.open herdr-share`, bound to a key by
// `herdr-share mod install`). It drives the herdr-share CLI:
//   - first run: set the relay URL (and token) for this computer;
//   - a terminal that is not shared: pick who can type and how long the
//     link works, then start sharing (the link is copied);
//   - a shared terminal: copy the link, switch view only / can type, extend,
//     see who is watching, stop sharing.
// The CLI keeps the share running in the background; closing this dialog
// changes nothing.

import { View, div } from "gpui-kit";
import { v_flex, h_flex, InputState, Input } from "gpui-base";
import { exec, readTextFile, appHome, theme, props, closeOverlay, focused, panes, spaces, notify, checking } from "herdr";

const MODES = [
  { id: "view", label: "View only", hint: "Viewers watch. Nobody else can type." },
  { id: "control", label: "Can type", hint: "Anyone with the link can type into this terminal as you." },
];

const TTLS = [
  { id: "30m", label: "30 min" },
  { id: "2h", label: "2 hours" },
  { id: "8h", label: "8 hours" },
  { id: "0", label: "No limit" },
];

function normalTTL(ttl) {
  if (!ttl) return "2h";
  const known = TTLS.find((x) => x.id === ttl);
  if (known) return known.id;
  if (["never", "none", "0s"].includes(ttl)) return "0";
  return ttl;
}

function remaining(expiresAt) {
  if (!expiresAt || expiresAt.startsWith("0001-")) return "No time limit";
  const s = Math.max(0, (Date.parse(expiresAt) - Date.now()) / 1000);
  if (s < 60) return "Ends in under a minute";
  const m = Math.round(s / 60);
  if (m < 60) return `Ends in ${m} min`;
  const h = Math.floor(m / 60);
  return m % 60 ? `Ends in ${h} h ${m % 60} min` : `Ends in ${h} h`;
}

function parseJSON(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function errorOf(r) {
  const j = parseJSON(r.stdout);
  if (j && j.error) return j.error;
  return (r.stderr || r.stdout || "herdr-share failed").trim().replace(/^herdr-share:\s*/, "");
}

export default class HerdrShare extends View {
  init(_props, cx) {
    this.handle = cx.focus_handle();
    this.handle.focus();
    this.bin = "herdr-share";
    this.home = "";
    this.loading = true;
    this.busy = null; // what is running: "start", "mode", "stop", ...
    this.error = null;
    this.cfg = null;
    this.shares = [];
    this.settings = false;
    this.target = props()?.terminal ?? focused()?.terminal ?? null;
    this.mode = "view";
    this.ttl = "2h";
    this.relayInput = InputState.new({ placeholder: "https://share.example.com" });
    this.tokenInput = InputState.new({ placeholder: "Only if your relay requires one" });
    this.tokenInput.set_masked(true);
    this.relayInput.on("submit", (_e, cx) => this.saveSettings(cx));
    this.tokenInput.on("submit", (_e, cx) => this.saveSettings(cx));
    if (checking()) {
      this.loading = false;
      return;
    }
    cx.spawn(async (cx) => {
      await this.locate();
      await this.reload();
      this.loading = false;
      cx.notify();
    });
    // Viewers come and go: refresh while the dialog is open.
    cx.timer.every(2500, (cx) => {
      if (this.busy || this.loading || this.refreshing) return;
      this.refreshing = true;
      cx.spawn(async (cx) => {
        try {
          await this.reloadShares();
          cx.notify();
        } finally {
          this.refreshing = false;
        }
      });
    });
  }

  // `herdr-share mod install` records where the CLI is, so Herdr's PATH
  // does not have to include it.
  async locate() {
    const text = await readTextFile(`${appHome()}/mods/herdr-share/install.json`).catch(() => null);
    const info = text ? parseJSON(text) : null;
    if (info?.bin) this.bin = info.bin;
    if (info?.home) this.home = info.home;
  }

  run(args, timeout = 30000) {
    const full = this.home ? ["--home", this.home, ...args] : args;
    return exec(this.bin, [...full, "--json"], { timeout }).catch((e) => ({
      code: 127,
      stdout: "",
      stderr: `Cannot run ${this.bin}: ${e}. Install herdr-share and run \`herdr-share mod install\`.`,
    }));
  }

  async reload() {
    const r = await this.run(["config", "show"]);
    if (r.code !== 0) {
      this.error = errorOf(r);
      this.cfg = null;
      return;
    }
    this.cfg = parseJSON(r.stdout) ?? {};
    this.mode = this.cfg.mode === "control" ? "control" : "view";
    this.ttl = normalTTL(this.cfg.ttl);
    this.relayInput.set_value(this.cfg.relay ?? "");
    await this.reloadShares();
  }

  async reloadShares() {
    const r = await this.run(["list"]);
    if (r.code === 0) this.shares = parseJSON(r.stdout) ?? [];
  }

  share() {
    return this.shares.find((s) => s.terminal === this.target) ?? null;
  }

  // The space and tab of the target, shown to viewers.
  label() {
    const pane = panes().find((p) => p.terminal === this.target);
    if (!pane) return "";
    for (const s of spaces()) {
      const tab = s.tabs.find((t) => t.id === pane.tab);
      if (tab) return [s.name, tab.name].filter(Boolean).join(" · ");
    }
    return "";
  }

  title() {
    const pane = panes().find((p) => p.terminal === this.target);
    return pane?.title || pane?.program?.name || this.target || "";
  }

  act(cx, what, fn) {
    if (this.busy) return;
    this.busy = what;
    this.error = null;
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        await fn();
      } catch (e) {
        this.error = String(e);
      } finally {
        this.busy = null;
        cx.notify();
      }
    });
  }

  saveSettings(cx) {
    const relay = this.relayInput.value().trim();
    const token = this.tokenInput.value().trim();
    if (!relay) {
      this.error = "Enter your relay's address.";
      cx.notify();
      return;
    }
    this.act(cx, "settings", async () => {
      let r = await this.run(["config", "set", "relay", relay]);
      if (r.code !== 0) throw errorOf(r);
      if (token) {
        r = await this.run(["config", "set", "token", token]);
        if (r.code !== 0) throw errorOf(r);
        this.tokenInput.set_value("");
      }
      r = await this.run(["doctor"], 20000);
      const report = parseJSON(r.stdout);
      const relayCheck = report?.checks?.find((c) => c.name === "relay");
      if (relayCheck && !relayCheck.ok) throw relayCheck.detail;
      this.settings = false;
      await this.reload();
    });
  }

  start(cx) {
    this.act(cx, "start", async () => {
      const args = ["start", this.target, "--mode", this.mode, "--ttl", this.ttl, "--copy"];
      const label = this.label();
      if (label) args.push("--label", label);
      const r = await this.run(args, 60000);
      if (r.code !== 0) throw errorOf(r);
      const s = parseJSON(r.stdout);
      await this.reloadShares();
      notify({
        title: s?.copied ? "Share link copied" : "Sharing started",
        body: s?.link ?? "",
        level: "success",
        timeout: 6000,
      });
    });
  }

  setMode(cx, s, mode) {
    if (s.mode === mode) return;
    this.act(cx, "mode", async () => {
      const r = await this.run(["mode", s.id, mode]);
      if (r.code !== 0) throw errorOf(r);
      await this.reloadShares();
    });
  }

  extend(cx, s, ttl) {
    this.act(cx, "extend", async () => {
      const r = await this.run(["extend", s.id, ttl]);
      if (r.code !== 0) throw errorOf(r);
      await this.reloadShares();
    });
  }

  copy(cx, s) {
    this.act(cx, "copy", async () => {
      const r = await this.run(["link", s.id, "--copy"]);
      if (r.code !== 0) throw errorOf(r);
      notify({ title: "Share link copied", body: s.link, level: "success", timeout: 4000 });
    });
  }

  stop(cx, s) {
    this.act(cx, `stop:${s.id}`, async () => {
      const r = await this.run(["stop", s.id]);
      if (r.code !== 0) throw errorOf(r);
      await this.reloadShares();
      notify({ title: "Sharing stopped", body: "The link no longer works.", level: "info", timeout: 4000 });
    });
  }

  // ---- view ----

  button(id, text, kind, onClick, opts = {}) {
    const t = theme();
    const colors = {
      primary: [t.solid, t.solid_foreground, t.solid],
      danger: [t.raised, t.blocked, t.input],
      plain: [t.raised, t.foreground, t.input],
      ghost: [null, t.muted, null],
    }[kind];
    const [bg, fg, border] = colors;
    const disabled = Boolean(opts.disabled || this.busy);
    let b = h_flex().id(id).h(30).px(12).gap(6).items_center().justify_center().rounded(7).text_size(12.5).font_medium().text_color(fg);
    if (bg) b = b.bg(bg);
    if (border) b = b.border_1().border_color(border);
    if (disabled) return b.opacity(0.5).child(opts.busy ? "Working…" : text);
    return b.cursor_pointer().hover((s) => s.opacity(0.86)).on_click((_e, cx) => onClick(cx)).child(text);
  }

  segmented(id, items, value, onPick) {
    const t = theme();
    return h_flex()
      .id(id)
      .p(2)
      .gap(2)
      .rounded(8)
      .bg(t.raised)
      .border_1()
      .border_color(t.border)
      .children(
        items.map((it) => {
          const on = it.id === value;
          let seg = h_flex().id(`${id}-${it.id}`).flex_1().h(26).px(10).items_center().justify_center().rounded(6).text_size(12.5);
          seg = on ? seg.bg(t.background).text_color(t.foreground).font_medium().border_1().border_color(t.border) : seg.text_color(t.muted);
          if (!on && !this.busy) seg = seg.cursor_pointer().hover((s) => s.text_color(t.foreground)).on_click((_e, cx) => onPick(it.id, cx));
          return seg.child(it.label);
        }),
      );
  }

  field(label, child, hint) {
    const t = theme();
    return v_flex()
      .gap(6)
      .child(div().text_size(11.5).text_color(t.dim).child(label))
      .child(child)
      .when(Boolean(hint), (el) => el.child(div().text_size(11.5).text_color(t.muted).child(hint)));
  }

  header(title, sub) {
    const t = theme();
    return h_flex()
      .items_start()
      .gap(12)
      .child(
        v_flex()
          .flex_1()
          .min_w(0)
          .gap(2)
          .child(div().text_size(15).font_semibold().text_color(t.foreground).child(title))
          .when(Boolean(sub), (el) => el.child(div().text_size(12).text_color(t.muted).truncate().child(sub))),
      )
      .child(div().text_size(11).text_color(t.dim).child("Esc"));
  }

  settingsView() {
    const t = theme();
    const input = (state) => Input.new(state).h(34).px(10).rounded(8).border_1().border_color(t.input).bg(t.background).text_size(12.8).text_color(t.foreground);
    const hasToken = Boolean(this.cfg?.has_token);
    return v_flex()
      .gap(16)
      .child(this.header("Relay", "Where shared sessions are relayed. Deploy your own with the herdr-share-relay image."))
      .child(this.field("Relay address", input(this.relayInput)))
      .child(this.field("Token", input(this.tokenInput), hasToken ? "A token is saved. Leave empty to keep it." : "Leave empty if your relay is open."))
      .child(
        h_flex()
          .gap(8)
          .justify_end()
          .when(Boolean(this.cfg?.relay), (el) => el.child(this.button("hs-settings-cancel", "Cancel", "plain", (cx) => { this.settings = false; this.error = null; cx.notify(); })))
          .child(this.button("hs-settings-save", "Save", "primary", (cx) => this.saveSettings(cx), { busy: this.busy === "settings" })),
      );
  }

  newShareView() {
    const t = theme();
    const mode = MODES.find((m) => m.id === this.mode);
    return v_flex()
      .gap(16)
      .child(this.header("Share this session", `${this.title()} · ${this.target}`))
      .child(this.field("Who can type", this.segmented("hs-mode", MODES, this.mode, (id, cx) => { this.mode = id; cx.notify(); }), mode.hint))
      .child(this.field("Link works for", this.segmented("hs-ttl", TTLS, this.ttl, (id, cx) => { this.ttl = id; cx.notify(); })))
      .when(this.mode === "control", (el) =>
        el.child(
          div()
            .p(10)
            .rounded(8)
            .bg(t.raised)
            .border_1()
            .border_color(t.blocked)
            .text_size(12)
            .text_color(t.foreground)
            .child("Viewers will be able to run commands in this terminal as you. Share the link only with people you trust; you can switch to view only at any time."),
        ),
      )
      .child(
        h_flex()
          .gap(8)
          .items_center()
          .child(div().flex_1().text_size(11.5).text_color(t.dim).truncate().child(`Relay: ${this.cfg?.relay ?? ""}`))
          .child(this.button("hs-start", "Start sharing", "primary", (cx) => this.start(cx), { busy: this.busy === "start" })),
      );
  }

  activeView(s) {
    const t = theme();
    const viewers = s.guests ?? [];
    return v_flex()
      .gap(16)
      .child(this.header("Sharing this session", `${s.title || this.title()} · ${s.terminal}`))
      .child(
        h_flex()
          .gap(8)
          .items_center()
          .child(
            div()
              .id("hs-link")
              .flex_1()
              .min_w(0)
              .h(32)
              .px(10)
              .rounded(8)
              .border_1()
              .border_color(t.input)
              .bg(t.background)
              .text_size(12)
              .text_color(t.muted)
              .flex()
              .items_center()
              .truncate()
              .child(s.link),
          )
          .child(this.button("hs-copy", "Copy link", "plain", (cx) => this.copy(cx, s))),
      )
      .child(this.field("Who can type", this.segmented("hs-live-mode", MODES, s.mode, (id, cx) => this.setMode(cx, s, id)), MODES.find((m) => m.id === s.mode)?.hint))
      .child(
        h_flex()
          .gap(8)
          .items_center()
          .child(div().flex_1().text_size(12.5).text_color(t.foreground).child(remaining(s.expires_at)))
          .child(this.button("hs-extend-30", "+30 min", "ghost", (cx) => this.extend(cx, s, "30m")))
          .child(this.button("hs-extend-2h", "2 hours from now", "ghost", (cx) => this.extend(cx, s, "2h"))),
      )
      .child(
        v_flex()
          .gap(6)
          .child(div().text_size(11.5).text_color(t.dim).child(viewers.length ? `Watching now (${viewers.length})` : "Nobody is watching yet"))
          .children(
            viewers.map((g) =>
              h_flex()
                .id(`hs-guest-${g.id}`)
                .gap(8)
                .items_center()
                .text_size(12.5)
                .child(div().size(6).rounded(3).bg(t.working))
                .child(div().text_color(t.foreground).child(g.name))
                .child(div().text_color(t.dim).child(g.client === "web" ? "browser" : g.client ?? "")),
            ),
          ),
      )
      .when(s.status === "reconnecting", (el) => el.child(div().text_size(12).text_color(t.blocked).child("Reconnecting to the relay…")))
      .child(
        h_flex()
          .gap(8)
          .justify_end()
          .child(this.button("hs-stop", "Stop sharing", "danger", (cx) => this.stop(cx, s), { busy: this.busy === `stop:${s.id}` })),
      );
  }

  othersView() {
    const t = theme();
    const others = this.shares.filter((s) => s.terminal !== this.target);
    if (!others.length) return null;
    return v_flex()
      .gap(6)
      .pt(12)
      .border_t_1()
      .border_color(t.border)
      .child(div().text_size(11.5).text_color(t.dim).child(`Other shared sessions (${others.length})`))
      .children(
        others.map((s) =>
          h_flex()
            .id(`hs-other-${s.id}`)
            .gap(8)
            .items_center()
            .text_size(12.5)
            .child(div().flex_1().min_w(0).truncate().text_color(t.foreground).child(`${s.title || s.terminal} · ${s.mode === "control" ? "can type" : "view only"} · ${(s.guests ?? []).length} watching`))
            .child(this.button(`hs-other-stop-${s.id}`, "Stop", "ghost", (cx) => this.stop(cx, s))),
        ),
      );
  }

  render() {
    const t = theme();
    let body;
    if (this.loading) {
      body = v_flex().gap(16).child(this.header("Share this session", "")).child(div().text_color(t.muted).child("Loading…"));
    } else if (!this.cfg) {
      body = v_flex().gap(12).child(this.header("Share this session", "")).child(div().text_color(t.muted).child("herdr-share is not available."));
    } else if (this.settings || !this.cfg.relay) {
      body = this.settingsView();
    } else if (!this.target) {
      body = v_flex().gap(12).child(this.header("Share this session", "")).child(div().text_color(t.muted).child("Focus a terminal, then open this again to share it."));
    } else {
      const s = this.share();
      body = s ? this.activeView(s) : this.newShareView();
    }
    const others = !this.loading && this.cfg?.relay && !this.settings ? this.othersView() : null;
    return v_flex()
      .id("herdr-share")
      .track_focus(this.handle)
      .on_key_down((e, cx) => {
        if (e.key === "escape") {
          closeOverlay(props().overlay);
          cx.stop_propagation();
        }
      })
      .w_full()
      .min_h(0)
      .p(18)
      .gap(14)
      .bg(t.popover)
      .text_size(13)
      .text_color(t.foreground)
      .child(body)
      .when(Boolean(this.error), (el) =>
        el.child(div().p(10).rounded(8).bg(t.raised).border_1().border_color(t.blocked).text_size(12).text_color(t.blocked).child(String(this.error))),
      )
      .when(Boolean(others), (el) => el.child(others))
      .when(Boolean(this.cfg?.relay) && !this.settings && !this.loading, (el) =>
        el.child(
          h_flex()
            .justify_end()
            .child(
              div()
                .id("hs-open-settings")
                .text_size(11.5)
                .text_color(t.dim)
                .cursor_pointer()
                .hover((s) => s.text_color(t.foreground))
                .on_click((_e, cx) => {
                  this.settings = true;
                  this.error = null;
                  cx.notify();
                })
                .child("Relay settings"),
            ),
        ),
      );
  }
}
