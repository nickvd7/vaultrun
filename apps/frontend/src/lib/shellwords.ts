/**
 * Splits a command-line style string into arguments, honouring single quotes,
 * double quotes and backslash escapes, the way a POSIX shell would (without any
 * expansion). `-c "print(2+2)"` becomes ["-c", "print(2+2)"].
 *
 * Throws on an unterminated quote so the caller can tell the user instead of
 * silently sending a mangled command.
 */
export function splitArgs(input: string): string[] {
  const out: string[] = [];
  let cur = "";
  let inToken = false;
  let quote: '"' | "'" | null = null;

  for (let i = 0; i < input.length; i++) {
    const ch = input[i];

    if (quote === "'") {
      if (ch === "'") quote = null;
      else cur += ch;
      continue;
    }

    if (quote === '"') {
      if (ch === '"') {
        quote = null;
      } else if (ch === "\\" && i + 1 < input.length && '"\\$`'.includes(input[i + 1])) {
        cur += input[++i];
      } else {
        cur += ch;
      }
      continue;
    }

    if (ch === "'" || ch === '"') {
      quote = ch;
      inToken = true;
    } else if (ch === "\\" && i + 1 < input.length) {
      cur += input[++i];
      inToken = true;
    } else if (/\s/.test(ch)) {
      if (inToken) {
        out.push(cur);
        cur = "";
        inToken = false;
      }
    } else {
      cur += ch;
      inToken = true;
    }
  }

  if (quote) throw new Error(`Unterminated ${quote === '"' ? "double" : "single"} quote in arguments`);
  if (inToken) out.push(cur);
  return out;
}

/** Sensible starting command for an image; python is not in most images. */
export function defaultCommandFor(image: string): { command: string; args: string } {
  const name = image.split("/").pop()?.split(":")[0]?.toLowerCase() ?? "";
  if (name.includes("python")) return { command: "python", args: '-c "print(2+2)"' };
  if (name.includes("node")) return { command: "node", args: '-e "console.log(2+2)"' };
  return { command: "sh", args: '-c "echo hello"' };
}

/**
 * Renders a command line for display, quoting like Python's shlex.quote: only
 * arguments made of plainly safe characters stay bare, so `print(2+2)` shows as
 * `'print(2+2)'` and can be pasted back into splitArgs unchanged.
 */
export function formatCommand(command: string, args: string[] | null | undefined): string {
  const quoted = (args ?? []).map((a) =>
    a === "" ? "''" : /^[A-Za-z0-9_@%+=:,./-]+$/.test(a) ? a : `'${a.replace(/'/g, `'\\''`)}'`
  );
  return [command, ...quoted].join(" ");
}
