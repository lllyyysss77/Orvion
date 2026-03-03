import type { CSSProperties } from "react";
import { Box, CodeXml, Copy, Sparkles, Terminal } from "lucide-react";
import CodeHighlight from "./CodeHighlight";
import type { CliSectionItem } from "./home-config";

type CliSectionProps = {
  item: CliSectionItem;
  darkMode: boolean;
  normalTextClass: string;
  panelClass: string;
  codePanelClass: string;
  onCopy: (value: string) => void;
  sectionRef: (el: HTMLElement | null) => void;
  badgeStyle: CSSProperties;
  titleStyle: CSSProperties;
  descStyle: CSSProperties;
  cardStyleFn: (cardIndex: number) => CSSProperties;
};

export default function CliSection({
  item,
  darkMode,
  normalTextClass,
  panelClass,
  codePanelClass,
  onCopy,
  sectionRef,
  badgeStyle,
  titleStyle,
  descStyle,
  cardStyleFn,
}: CliSectionProps) {
  const badgeIcon =
    item.id === "claude" ? (
      <CodeXml className="h-3 w-3" />
    ) : item.id === "openai" ? (
      <Terminal className="h-3 w-3" />
    ) : (
      <Sparkles className="h-3 w-3" />
    );

  const platformIcon = item.id === "claude" ? <Terminal className="h-4 w-4" /> : <Box className="h-4 w-4" />;

  return (
    <section
      id={item.id}
      ref={sectionRef}
      className="min-h-screen snap-start flex items-center justify-center px-4 py-20 sm:px-8 md:px-16 lg:px-20"
    >
      <div className="mx-auto grid w-full max-w-7xl items-center gap-12 md:grid-cols-2">
        <div className={`${item.reverse ? "lg:order-2" : ""} max-w-[760px]`}>
          <div
            className="mb-5 inline-flex items-center gap-2 rounded-full border border-[#cc785c]/20 bg-[#cc785c]/10 px-4 py-1.5 text-xs font-medium text-[#cc785c]"
            style={badgeStyle}
          >
            {badgeIcon}
            {item.badge}
          </div>

          <h2 className="mb-6 font-serif text-2xl font-semibold leading-[1.08] md:text-3xl" style={titleStyle}>
            {item.title}
          </h2>
          <p className={`mb-6 max-w-[760px] text-sm leading-[1.45] md:text-lg ${normalTextClass}`} style={descStyle}>
            {item.description}
          </p>

          <div className={`mb-4 rounded-2xl border px-4 py-3 ${panelClass}`} style={cardStyleFn(0)}>
            <div className="flex flex-wrap items-center gap-3">
              <div className={`flex h-12 shrink-0 items-center gap-2 rounded-2xl border px-4 ${darkMode ? "border-[rgba(227,224,211,0.12)]" : "border-[#e5e4df]"}`}>
                <span className="text-[#cc785c]">{platformIcon}</span>
                <div className="text-left">
                  <p className="text-[11px] font-semibold leading-none md:text-xs">{item.installLabel}</p>
                  <p className={`text-[8px] ${normalTextClass}`}>{item.installHint}</p>
                </div>
              </div>
              <div className={`min-w-[180px] flex-1 rounded-2xl border px-4 py-2 ${codePanelClass} ${darkMode ? "border-[rgba(227,224,211,0.12)]" : "border-[#e5e4df]"}`}>
                <CodeHighlight code={item.installCommand} language="bash" className="overflow-x-auto text-[11px] leading-6 md:text-xs" />
              </div>
              <button
                className={`flex h-9 w-9 items-center justify-center rounded-xl border transition ${darkMode ? "border-[rgba(227,224,211,0.12)] hover:bg-[#312c26]" : "border-[#e5e4df] hover:bg-[#f0f0eb]"}`}
                onClick={() => onCopy(item.installCommand)}
                title="复制配置"
              >
                <Copy className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>

          <div className={`mb-3 overflow-hidden rounded-2xl border ${panelClass}`} style={cardStyleFn(1)}>
            <div className={`flex items-center justify-between border-b px-4 py-3 ${darkMode ? "border-[rgba(227,224,211,0.12)]" : "border-[#e5e4df]"}`}>
              <span className={`font-mono text-[11px] font-medium md:text-xs ${normalTextClass}`}>{item.configPath}</span>
              <button
                className={`flex h-9 w-9 items-center justify-center rounded-xl border transition ${darkMode ? "border-[rgba(227,224,211,0.12)] hover:bg-[#312c26]" : "border-[#e5e4df] hover:bg-[#f0f0eb]"}`}
                onClick={() => onCopy(item.configContent)}
                title="复制配置"
              >
                <Copy className="h-3.5 w-3.5" />
              </button>
            </div>
            <div className={`${codePanelClass} p-4`}>
              <CodeHighlight
                code={item.configContent}
                language={item.configLang}
                className="max-h-[280px] overflow-x-auto overflow-y-auto font-mono text-[11px] leading-[1.65] md:text-xs"
              />
            </div>
          </div>

          {item.extraConfigPath && item.extraConfigContent && (
            <div className={`overflow-hidden rounded-2xl border ${panelClass}`} style={cardStyleFn(2)}>
              <div className={`flex items-center justify-between border-b px-4 py-3 ${darkMode ? "border-[rgba(227,224,211,0.12)]" : "border-[#e5e4df]"}`}>
                <span className={`font-mono text-[11px] font-medium md:text-xs ${normalTextClass}`}>{item.extraConfigPath}</span>
                <button
                  className={`flex h-9 w-9 items-center justify-center rounded-xl border transition ${darkMode ? "border-[rgba(227,224,211,0.12)] hover:bg-[#312c26]" : "border-[#e5e4df] hover:bg-[#f0f0eb]"}`}
                  onClick={() => onCopy(item.extraConfigContent || "")}
                  title="复制配置"
                >
                  <Copy className="h-3.5 w-3.5" />
                </button>
              </div>
              <div className={`${codePanelClass} p-4`}>
                <CodeHighlight
                  code={item.extraConfigContent}
                  language={item.extraConfigLang || "json"}
                  className="max-h-[280px] overflow-x-auto overflow-y-auto font-mono text-[11px] leading-[1.65] md:text-xs"
                />
              </div>
            </div>
          )}
        </div>

        <div
          className={`${item.reverse ? "lg:order-1" : ""} hidden min-h-[520px] items-center justify-center lg:flex`}
        />
      </div>
    </section>
  );
}
