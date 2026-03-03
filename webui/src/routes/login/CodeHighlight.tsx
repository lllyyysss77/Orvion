import type { CodeLang } from "./home-config";

type CodeHighlightProps = {
  code: string;
  language: CodeLang;
  className?: string;
};

const escapeHtml = (value: string): string =>
  value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");

const highlightPlaceholders = (value: string): string =>
  value.replace(
    /\b(your-api-key|latest-model-name|your-domain)\b/g,
    `<span class="text-[#d7545a] italic">$1</span>`
  );

const highlightBashHtml = (value: string): string => {
  let html = escapeHtml(value);
  html = html.replace(/\b(curl|npm|bash)\b/g, `<span class="text-[#d26f4b] font-medium">$1</span>`);
  html = html.replace(/(\s)(-[\w-]+)/g, `$1<span class="text-[#d26f4b]">$2</span>`);
  html = html.replace(/(\s)\|(\s)/g, `$1<span class="text-[#d26f4b]">|</span>$2`);
  return highlightPlaceholders(html);
};

const highlightCodeHtml = (value: string, lang: Exclude<CodeLang, "bash">): string => {
  let html = escapeHtml(value);
  if (lang === "json") {
    html = html.replace(/"([^"\n]+)"(?=\s*:)/g, `<span class="text-[#d26f4b]">"$1"</span>`);
  }

  if (lang === "toml" || lang === "env") {
    html = html.replace(/^([\w.\-\[\]]+)(\s*=)/gm, `<span class="text-[#d26f4b]">$1</span>$2`);
    if (lang === "toml") {
      html = html.replace(/^\[([^\]\n]+)\]/gm, `<span class="text-[#d26f4b]">[$1]</span>`);
    }
  }

  return highlightPlaceholders(html);
};

export default function CodeHighlight({ code, language, className }: CodeHighlightProps) {
  const html = language === "bash" ? highlightBashHtml(code) : highlightCodeHtml(code, language);

  return (
    <pre className={className}>
      <code dangerouslySetInnerHTML={{ __html: html }} />
    </pre>
  );
}
