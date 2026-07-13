import { Fragment, type ReactNode } from "react";

// resolveImg maps a Markdown image src to the URL to actually load. Reports live
// off the web root, so a bare screenshot reference (`shot.png`, `.ultraflow/shots/
// shot.png`) has to be rewritten to the task's shots endpoint; callers pass a
// resolver for that. The default leaves the src untouched (absolute/remote URLs).
type ResolveImg = (src: string) => string;
const identity: ResolveImg = (src) => src;

// Markdown renders an agent's report — well-formed Markdown from a Claude/Codex
// run — as native, design-system-styled React. Deliberately small: it covers the
// blocks agents actually emit (headings, lists, code fences, quotes, rules,
// paragraphs) plus inline bold/italic/code/links/images, and nothing more. No dep,
// no dangerouslySetInnerHTML — the report is local, trusted text, but we still
// only ever render it as text nodes and known elements. `resolveImg` rewrites
// image srcs so a report can embed the screenshots the agent saved for the task.
export function Markdown({ text, resolveImg = identity }: { text: string; resolveImg?: ResolveImg }) {
  return (
    <div className="report-prose flex flex-col gap-3">{blocks(text, resolveImg)}</div>
  );
}

// blocks splits the source into block-level chunks and renders each. Fenced code
// is consumed greedily so its contents are never parsed as Markdown.
function blocks(src: string, resolveImg: ResolveImg): ReactNode[] {
  const lines = src.replace(/\r\n/g, "\n").split("\n");
  const out: ReactNode[] = [];
  let i = 0;
  let key = 0;

  while (i < lines.length) {
    const line = lines[i];

    // fenced code block
    const fence = line.match(/^```(.*)$/);
    if (fence) {
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) body.push(lines[i++]);
      i++; // closing fence
      out.push(
        <pre
          key={key++}
          className="overflow-x-auto rounded-lg border border-white/10 bg-[#17171A] p-3 font-mono text-[12px] leading-[1.55] text-[#ECECEA]"
        >
          {body.join("\n")}
        </pre>,
      );
      continue;
    }

    // blank line — skip (gap handled by flex container)
    if (line.trim() === "") {
      i++;
      continue;
    }

    // horizontal rule
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(line.trim())) {
      out.push(<hr key={key++} className="border-hairline" />);
      i++;
      continue;
    }

    // heading
    const h = line.match(/^(#{1,6})\s+(.*)$/);
    if (h) {
      const level = h[1].length;
      const size =
        level <= 1 ? "text-[18px]" : level === 2 ? "text-[16px]" : "text-[14px]";
      out.push(
        <div
          key={key++}
          className={`${size} font-semibold leading-snug text-ink ${level >= 3 ? "mt-1" : "mt-2"}`}
        >
          {inline(h[2], resolveImg)}
        </div>,
      );
      i++;
      continue;
    }

    // blockquote (consume consecutive > lines)
    if (/^>\s?/.test(line)) {
      const quote: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i]))
        quote.push(lines[i++].replace(/^>\s?/, ""));
      out.push(
        <blockquote
          key={key++}
          className="border-l-2 border-hairline pl-3 text-[13px] italic leading-relaxed text-muted"
        >
          {quote.map((q, n) => (
            <p key={n}>{inline(q, resolveImg)}</p>
          ))}
        </blockquote>,
      );
      continue;
    }

    // GFM table: a `| a | b |` header, a `|---|---|` separator, then body rows.
    // Gated on the separator so ordinary prose with a stray "|" stays a paragraph.
    if (tableStartsAt(lines, i)) {
      const header = tableCells(line);
      i += 2; // header + separator
      const body: string[][] = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim() !== "") {
        body.push(tableCells(lines[i++]));
      }
      out.push(
        <div key={key++} className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px] text-ink">
            <thead>
              <tr>
                {header.map((c, n) => (
                  <th key={n} className="border border-hairline bg-board px-2.5 py-1.5 text-left font-semibold">
                    {inline(c, resolveImg)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {body.map((row, r) => (
                <tr key={r}>
                  {header.map((_, n) => (
                    <td key={n} className="border border-hairline px-2.5 py-1.5 align-top">
                      {inline(row[n] ?? "", resolveImg)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }

    // list (unordered or ordered) — consume consecutive item lines
    if (/^\s*([-*+]|\d+\.)\s+/.test(line)) {
      const ordered = /^\s*\d+\.\s+/.test(line);
      const items: string[] = [];
      while (i < lines.length && /^\s*([-*+]|\d+\.)\s+/.test(lines[i]))
        items.push(lines[i++].replace(/^\s*([-*+]|\d+\.)\s+/, ""));
      const cls = "flex flex-col gap-1 pl-5 text-[13px] leading-relaxed text-ink";
      out.push(
        ordered ? (
          <ol key={key++} className={`list-decimal ${cls}`}>
            {items.map((it, n) => (
              <li key={n} className="pl-1">
                {inline(it, resolveImg)}
              </li>
            ))}
          </ol>
        ) : (
          <ul key={key++} className={`list-disc ${cls}`}>
            {items.map((it, n) => (
              <li key={n} className="pl-1">
                {inline(it, resolveImg)}
              </li>
            ))}
          </ul>
        ),
      );
      continue;
    }

    // paragraph — gather until a blank line or a block starter
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^```/.test(lines[i]) &&
      !/^(#{1,6})\s/.test(lines[i]) &&
      !/^>\s?/.test(lines[i]) &&
      !/^\s*([-*+]|\d+\.)\s+/.test(lines[i]) &&
      !/^(-{3,}|\*{3,}|_{3,})$/.test(lines[i].trim()) &&
      !tableStartsAt(lines, i)
    ) {
      para.push(lines[i++]);
    }
    out.push(
      <p key={key++} className="text-[13px] leading-relaxed text-ink">
        {inline(para.join(" "), resolveImg)}
      </p>,
    );
  }

  return out;
}

// tableStartsAt reports whether a GFM table begins at line idx: a row containing a
// pipe, immediately followed by a `---|---` separator (dashes, pipes, optional
// alignment colons). Requiring the separator keeps a stray "|" in prose from
// tripping table mode.
function tableStartsAt(lines: string[], idx: number): boolean {
  return (
    idx + 1 < lines.length &&
    lines[idx].includes("|") &&
    lines[idx + 1].includes("|") &&
    // separator row: only dashes/colons/pipes/space, with at least one dash. The
    // pipe requirement above means this never matches a plain `---` rule.
    /^[\s:|-]*-[\s:|-]*$/.test(lines[idx + 1])
  );
}

// tableCells splits one table row into trimmed cells, dropping the empty cells the
// leading/trailing pipes produce.
function tableCells(row: string): string[] {
  let s = row.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  return s.split("|").map((c) => c.trim());
}

// inline renders bold, italic, inline code, links, and images within one line. It
// walks the string with a single alternation so the earliest match wins and the
// rest is re-scanned — keeps precedence sane without a full parser. The image
// alternative precedes the link one so `![alt](src)` isn't misread as `!` + link.
function inline(src: string, resolveImg: ResolveImg): ReactNode {
  const nodes: ReactNode[] = [];
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(__[^_]+__)|(\*[^*]+\*)|(_[^_]+_)|(!\[[^\]]*\]\([^)]+\))|(\[[^\]]+\]\([^)]+\))/;
  let rest = src;
  let key = 0;

  while (rest.length) {
    const m = rest.match(re);
    if (!m || m.index === undefined) {
      nodes.push(<Fragment key={key++}>{rest}</Fragment>);
      break;
    }
    if (m.index > 0) nodes.push(<Fragment key={key++}>{rest.slice(0, m.index)}</Fragment>);
    const tok = m[0];

    if (tok.startsWith("`")) {
      nodes.push(
        <code
          key={key++}
          className="rounded bg-board px-1 py-0.5 font-mono text-[12px] text-ink"
        >
          {tok.slice(1, -1)}
        </code>,
      );
    } else if (tok.startsWith("**") || tok.startsWith("__")) {
      nodes.push(
        <strong key={key++} className="font-semibold text-ink">
          {tok.slice(2, -2)}
        </strong>,
      );
    } else if (tok.startsWith("![")) {
      const im = tok.match(/^!\[([^\]]*)\]\(([^)]+)\)$/)!;
      nodes.push(
        <a
          key={key++}
          href={resolveImg(im[2])}
          target="_blank"
          rel="noreferrer"
          className="my-1 block overflow-hidden rounded-lg border border-hairline bg-surface transition hover:border-ink/25"
        >
          <img
            src={resolveImg(im[2])}
            alt={im[1]}
            className="max-h-80 w-full object-contain bg-[#17171A]"
          />
          {im[1] && (
            <span className="block truncate px-2 py-1 text-[11px] text-muted">{im[1]}</span>
          )}
        </a>,
      );
    } else if (tok.startsWith("[")) {
      const lm = tok.match(/^\[([^\]]+)\]\(([^)]+)\)$/)!;
      nodes.push(
        <a
          key={key++}
          href={lm[2]}
          target="_blank"
          rel="noreferrer"
          className="text-steel underline decoration-steel/40 underline-offset-2 hover:decoration-steel"
        >
          {lm[1]}
        </a>,
      );
    } else {
      nodes.push(
        <em key={key++} className="italic">
          {tok.slice(1, -1)}
        </em>,
      );
    }
    rest = rest.slice(m.index + tok.length);
  }

  return nodes;
}
