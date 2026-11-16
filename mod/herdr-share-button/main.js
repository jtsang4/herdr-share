// herdr-share: a small floating Share button, the visible way into sharing.
//
// It floats over the window (drag it by its left edge; Herdr remembers where
// you leave it and brings it back on the next start). Click it to open the
// Share dialog for the focused terminal. While anything is shared it says so,
// and how many people are watching. × hides it; the Share dialog (⌘⇧S) can
// show it again.

import { View, div } from "gpui-kit";
import { h_flex } from "gpui-base";
import { exec, readTextFile, appHome, theme, run, props, closeOverlay, setShared, shared, focused, checking, watchFolder } from "herdr";

const KEY = "herdr-share.button";

function parseJSON(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

export default class ShareButton extends View {
  init(_props, cx) {
    this.shares = [];
    this.bin = "herdr-share";
    this.home = "";
    if (checking()) return;
    setShared(KEY, "shown");
    cx.spawn(async (cx) => {
      const text = await readTextFile(`${appHome()}/mods/herdr-share/install.json`).catch(() => null);
      const info = text ? parseJSON(text) : null;
      if (info?.bin) this.bin = info.bin;
      if (info?.home) this.home = info.home;
      await this.refresh();
      cx.notify();
      // Shares are files in herdr-share's state folder: follow them.
      const paths = parseJSON((await this.run(["config", "path"])).stdout);
      if (!paths?.state_dir) return;
      for (;;) {
        await watchFolder(`${paths.state_dir}/shares`).catch(() => null);
        await this.refresh();
        cx.notify();
      }
    });
    // A bridge that died without cleaning up is noticed by `list`.
    cx.timer.every(15000, (cx) => {
      cx.spawn(async (cx) => {
        await this.refresh();
        cx.notify();
      });
    });
  }

  async close() {
    if (shared(KEY) === "shown") setShared(KEY, null);
  }

  run(args) {
    const full = this.home ? ["--home", this.home, ...args] : args;
    return exec(this.bin, [...full, "--json"], { timeout: 15000 }).catch(() => ({ code: 127, stdout: "", stderr: "" }));
  }

  async refresh() {
    const r = await this.run(["list"]);
    if (r.code === 0) this.shares = parseJSON(r.stdout) ?? [];
  }

  render() {
    const t = theme();
    const target = focused()?.terminal ?? null;
    const mine = this.shares.find((s) => s.terminal === target);
    const watching = this.shares.reduce((n, s) => n + (s.guests ?? []).length, 0);
    const active = this.shares.length > 0;
    let label = "Share";
    if (mine) label = `Sharing · ${(mine.guests ?? []).length} watching`;
    else if (active) label = `Share · ${this.shares.length} shared, ${watching} watching`;

    const stop = (_e, cx) => cx.stop_propagation();
    return h_flex()
      .id("hs-button")
      .h(28)
      .items_center()
      .rounded(14)
      .bg(t.popover)
      .border_1()
      .border_color(active ? t.blocked : t.input)
      .shadow_md()
      .text_size(12.5)
      .text_color(t.foreground)
      // The grip: presses here are not stopped, so they drag the window.
      .child(div().id("hs-button-grip").w(10).h_full().ml(4).flex().items_center().justify_center().text_color(t.dim).child("⋮"))
      .child(
        h_flex()
          .id("hs-button-open")
          .h_full()
          .pl(4)
          .pr(8)
          .gap(6)
          .items_center()
          .cursor_pointer()
          .hover((s) => s.text_color(t.accent))
          .on_mouse_down("left", stop)
          .on_click(() => run("mod.open", { name: "herdr-share" }))
          .child(div().size(7).rounded(4).bg(active ? t.blocked : t.dim))
          .child(div().font_medium().child(label)),
      )
      .child(
        div()
          .id("hs-button-hide")
          .h_full()
          .pr(9)
          .flex()
          .items_center()
          .text_size(11)
          .text_color(t.dim)
          .cursor_pointer()
          .hover((s) => s.text_color(t.foreground))
          .on_mouse_down("left", stop)
          .on_click(() => {
            setShared(KEY, "hidden");
            closeOverlay(props().overlay);
          })
          .child("×"),
      );
  }
}
