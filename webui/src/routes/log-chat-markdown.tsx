import { useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Button } from "@/components/ui/button";

const MARKDOWN_PAGE_SIZE = 50_000;

export default function MarkdownOutputText({ value }: { value: string }) {
  const [visibleLength, setVisibleLength] = useState(MARKDOWN_PAGE_SIZE);

  useEffect(() => {
    setVisibleLength(MARKDOWN_PAGE_SIZE);
  }, [value]);

  const visibleValue = value.slice(0, visibleLength);
  const hasMore = visibleLength < value.length;

  return (
    <>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          p: ({ children }) => <p className="mb-3 leading-7 last:mb-0">{children}</p>,
          code: ({ children, className, ...props }) => {
            const isBlock = typeof className === "string" && className.includes("language-");
            if (isBlock) {
              return <code className={className} {...props}>{children}</code>;
            }
            return (
              <code className="rounded-md bg-muted px-2 py-0.5 font-mono text-[0.95em] text-foreground" {...props}>
                {children}
              </code>
            );
          },
        }}
      >
        {visibleValue}
      </ReactMarkdown>
      {hasMore && (
        <div className="mt-3 flex items-center gap-3 border-t pt-3">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setVisibleLength((current) => Math.min(value.length, current + MARKDOWN_PAGE_SIZE))}
          >
            继续显示
          </Button>
          <span className="text-xs text-muted-foreground">
            已显示 {visibleValue.length.toLocaleString()} / {value.length.toLocaleString()} 字符
          </span>
        </div>
      )}
    </>
  );
}
