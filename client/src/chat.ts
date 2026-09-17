// Minimal dependency-free chat TUI: full-screen redraw with a scrollable
// message area, a status line, and a single-line input box at the bottom.

import type { ChatMessage } from "./api.ts";

const ESC = "\x1b[";
const CLEAR = `${ESC}2J${ESC}H`;
const GRAY = `${ESC}90m`;
const LIGHT_BLUE = `${ESC}94m`;
const RESET = `${ESC}0m`;
// 256-color palette for sender names (warm/mixed hues; skips blues, used by
// message content, and grays, used by metadata). Names are assigned colors in
// round-robin order on first sight, so up to 12 distinct names always get
// distinct colors, and each name keeps its color for the whole session.
const NAME_COLORS = [196, 208, 214, 220, 154, 82, 201, 207, 218, 177, 189, 229];

export interface ChatUIOptions {
  title: string;
  selfName: string;
  selfId: number;
}

export class ChatUI {
  private readonly options: ChatUIOptions;
  private lines: string[] = [];
  private status = "connecting";
  private input = "";
  private onLine: ((line: string) => void) | null = null;
  private running = false;
  private readonly nameColors = new Map<string, number>();

  constructor(options: ChatUIOptions) {
    this.options = options;
  }

  start(onLine: (line: string) => void): void {
    this.onLine = onLine;
    this.running = true;
    process.stdin.setRawMode(true);
    process.stdin.resume();
    process.stdin.on("data", this.handleKey);
    process.stdout.on("resize", this.render);
    this.render();
  }

  stop(): void {
    this.running = false;
    process.stdin.off("data", this.handleKey);
    process.stdout.off("resize", this.render);
    process.stdin.setRawMode(false);
    process.stdin.pause();
    process.stdout.write(`${CLEAR}`);
  }

  setStatus(status: string): void {
    this.status = status;
    this.render();
  }

  setTitle(title: string): void {
    this.options.title = title;
    this.render();
  }

  addSystem(text: string): void {
    this.pushLines([`${GRAY}── ${text} ──${RESET}`]);
  }

  addMessage(msg: ChatMessage, selfId: number): void {
    const who =
      msg.sender.id === selfId
        ? "you"
        : (msg.sender.nickname ?? msg.sender.username);
    const time = new Date(msg.created_at).toLocaleTimeString();
    const marker = msg.sender.id === selfId ? "→" : " ";
    const body = msg.content.split("\n");
    this.pushLines([
      `${GRAY}${marker} [${time}] ${RESET}${this.colorForName(who)}${who}${RESET}${GRAY} (#${msg.sequence}):${RESET} ${LIGHT_BLUE}${body[0]}${RESET}`,
      ...body.slice(1).map((l) => `${LIGHT_BLUE}    ${l}${RESET}`),
    ]);
  }

  private colorForName(name: string): string {
    let color = this.nameColors.get(name);
    if (color === undefined) {
      color = NAME_COLORS[this.nameColors.size % NAME_COLORS.length];
      this.nameColors.set(name, color);
    }
    return `${ESC}1;38;5;${color}m`;
  }

  replaceMessage(msg: ChatMessage): void {
    // Moderation masking: the content is replaced with "***" server-side.
    // Simplest correct rendering: redraw from the stored API messages would
    // need full history; instead note the change inline.
    this.addSystem(`message #${msg.sequence} was moderated → ${msg.content}`);
  }

  private pushLines(newLines: string[]): void {
    this.lines.push(...newLines);
    if (this.lines.length > 2000) this.lines.splice(0, this.lines.length - 2000);
    this.render();
  }

  private handleKey = (data: Buffer): void => {
    const text = data.toString("utf8");
    for (const ch of text) {
      if (ch === "\x03" || ch === "\x04") {
        this.stop();
        process.exit(0);
      } else if (ch === "\r" || ch === "\n") {
        const line = this.input;
        this.input = "";
        if (line.trim().length > 0) this.onLine?.(line);
      } else if (ch === "\x7f") {
        this.input = [...this.input].slice(0, -1).join("");
      } else if (ch >= " " || ch.charCodeAt(0) > 0x7f) {
        this.input += ch;
      }
      // other control sequences (arrows etc.) are ignored
    }
    this.render();
  };

  private render = (): void => {
    if (!this.running) return;
    const cols = process.stdout.columns || 80;
    const rows = process.stdout.rows || 24;
    const statusColor = this.status === "online" ? `${ESC}92m` : GRAY;
    const header = [
      `${GRAY} Airway IM · ${this.options.title}${RESET}`,
      `${GRAY} you: ${RESET}${this.colorForName(this.options.selfName)}${this.options.selfName}${RESET}${GRAY} (id ${this.options.selfId}) · status: ${RESET}${statusColor}${this.status}${RESET}${GRAY} · /help for commands${RESET}`,
      `${GRAY}${"─".repeat(cols)}${RESET}`,
    ];
    const footerRows = 2; // separator + input
    const bodyRows = Math.max(rows - header.length - footerRows, 1);
    const visible = this.lines.slice(-bodyRows).map((l) => truncate(l, cols));
    while (visible.length < bodyRows) visible.unshift("");

    const inputLine = truncate(`> ${this.input}`, cols);
    const out = [
      CLEAR,
      ...header.map((l) => truncate(l, cols)),
      ...visible,
      `${GRAY}${"─".repeat(cols)}${RESET}`,
      inputLine,
    ].join("\r\n");
    process.stdout.write(out);
  };
}

// ANSI-aware truncation: escape sequences don't count toward the width; an
// over-long line falls back to its stripped plain text.
function truncate(s: string, cols: number): string {
  const plain = s.replace(/\x1b\[[0-9;]*m/g, "");
  if ([...plain].length <= cols) return s;
  return [...plain].slice(0, cols - 1).join("") + "…" + RESET;
}
