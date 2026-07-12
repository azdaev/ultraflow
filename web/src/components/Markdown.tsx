import { Fragment, type ReactNode } from "react";

// Markdown renders an agent's report — well-formed Markdown from a Claude/Codex
// run — as native, design-system-styled React. Deliberately small: it covers the
// blocks agents actually emit (headings, lists, code fences, quotes, rules,
// paragraphs) plus inline bold/italic/code/links, and nothing more. No dep, no
// dangerouslySetInnerHTML — the report is local, trusted text, but we still only
// ever render it as text nodes and known elements.
export function Markdown({ text }: { text: string }) {
  return <div className="report-prose flex flex-col gap-3">{blocks(text)}</div>;
}

// blocks splits the source into block-level chunks and renders each. Fenced code
// is consumed greedily so its contents are never parsed as Markdown.
function blocks(src: string): ReactNode[] {
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
          {inline(h[2])}
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
            <p key={n}>{inline(q)}</p>
          ))}
        </blockquote>,
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
                {inline(it)}
              </li>
            ))}
          </ol>
        ) : (
          <ul key={key++} className={`list-disc ${cls}`}>
            {items.map((it, n) => (
              <li key={n} className="pl-1">
                {inline(it)}
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
      !/^(-{3,}|\*{3,}|_{3,})$/.test(lines[i].trim())
    ) {
      para.push(lines[i++]);
    }
    out.push(
      <p key={key++} className="text-[13px] leading-relaxed text-ink">
        {inline(para.join(" "))}
      </p>,
    );
  }

  return out;
}

// inline renders bold, italic, inline code, and links within one line. It walks
// the string with a single alternation so the earliest match wins and the rest is
// re-scanned — keeps precedence sane without a full parser.
function inline(src: string): ReactNode {
  const nodes: ReactNode[] = [];
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(__[^_]+__)|(\*[^*]+\*)|(_[^_]+_)|(\[[^\]]+\]\([^)]+\))/;
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
