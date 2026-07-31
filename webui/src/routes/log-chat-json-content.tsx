import { useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { PrismLight as SyntaxHighlighter } from "react-syntax-highlighter";
import jsonLanguage from "react-syntax-highlighter/dist/esm/languages/prism/json";
import { duotoneLight } from "react-syntax-highlighter/dist/esm/styles/prism";

SyntaxHighlighter.registerLanguage("json", jsonLanguage);

interface JsonContentProps {
  text: string;
  parsed: boolean;
  empty: boolean;
}

const SYNTAX_HIGHLIGHT_LIMIT = 200_000;

function VirtualizedText({ text }: { text: string }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const lines = text.split("\n");
  const virtualizer = useVirtualizer({
    count: lines.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 24,
    overscan: 12,
  });

  return (
    <div ref={scrollRef} className="h-[min(60vh,32rem)] overflow-auto rounded-md border bg-muted/70 font-mono text-sm">
      <div className="relative min-w-max" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((row) => (
          <div
            key={row.key}
            className="absolute left-0 top-0 min-w-full whitespace-pre px-4 leading-6"
            style={{ transform: `translateY(${row.start}px)` }}
          >
            {lines[row.index] || " "}
          </div>
        ))}
      </div>
    </div>
  );
}

export default function JsonContent({ text, parsed, empty }: JsonContentProps) {
  if (text.length > SYNTAX_HIGHLIGHT_LIMIT) {
    return <VirtualizedText text={text} />;
  }

  if (parsed && !empty) {
    return (
      <div className="w-full max-w-full min-w-0 overflow-x-auto rounded-md border bg-muted/70 font-mono text-sm leading-6">
        <SyntaxHighlighter
          language="json"
          style={duotoneLight}
          customStyle={{
            margin: 0,
            background: "transparent",
            padding: "1rem",
            fontSize: "0.875rem",
            lineHeight: "1.5rem",
            whiteSpace: "pre",
            minWidth: "100%",
            maxWidth: "100%",
          }}
        >
          {text}
        </SyntaxHighlighter>
      </div>
    );
  }

  return (
    <pre className="whitespace-pre-wrap break-all rounded-md border bg-muted/70 p-4 font-mono text-sm leading-6 overflow-x-auto w-full max-w-full min-w-0">
      {text}
    </pre>
  );
}
