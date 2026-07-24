import { useRef, useState } from "react";
import { SendHorizontal, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { assessDanger } from "@/lib/danger";
import { cn } from "@/lib/utils";

/**
 * Message/command composer (SPEC §14.3). A plain text field, so the OS keyboard's
 * own voice dictation works for free. Danger-pattern input flips Send into a
 * two-tap confirm (advisory only — never pretends to sandbox the shell).
 */
export function Composer({
  onSubmit,
  placeholder = "message / command…",
  disabled = false,
}: {
  onSubmit: (text: string) => void;
  placeholder?: string;
  disabled?: boolean;
}) {
  const [value, setValue] = useState("");
  const [armed, setArmed] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const danger = assessDanger(value);

  function submit() {
    const text = value;
    if (!text.trim()) return;
    if (danger.danger && !armed) {
      setArmed(true);
      return;
    }
    onSubmit(text);
    setValue("");
    setArmed(false);
    inputRef.current?.focus();
  }

  return (
    <div className="flex flex-col gap-1 border-t border-frame bg-bulkhead px-2 pb-[calc(8px+var(--spacing-safe-bottom))] pt-2">
      {danger.danger && (
        <div
          className="flex items-center gap-1.5 px-1 text-[12px] text-flare"
          role="status"
          aria-live="polite"
        >
          <TriangleAlert className="size-3.5" />
          Danger: {danger.reason}. Tap Send again to confirm.
        </div>
      )}
      <div className="flex items-end gap-2">
        <textarea
          ref={inputRef}
          value={value}
          disabled={disabled}
          rows={1}
          inputMode="text"
          autoCapitalize="off"
          autoCorrect="off"
          spellCheck={false}
          aria-label="Message or command"
          placeholder={placeholder}
          onChange={(e) => {
            setValue(e.target.value);
            setArmed(false);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              submit();
            }
          }}
          className={cn(
            "max-h-28 min-h-11 flex-1 resize-none rounded-[10px] border bg-hull px-3 py-2.5 text-sm text-mist",
            "placeholder:text-muted-ink/70 focus-visible:outline-2 focus-visible:outline-brass",
            danger.danger ? "border-flare/60" : "border-frame",
          )}
        />
        <Button
          variant={danger.danger && armed ? "danger" : "primary"}
          size="icon"
          onClick={submit}
          disabled={disabled || !value.trim()}
          aria-label={danger.danger && armed ? "Confirm send" : "Send"}
        >
          <SendHorizontal className="size-5" />
        </Button>
      </div>
    </div>
  );
}
