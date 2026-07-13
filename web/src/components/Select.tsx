import type { ReactNode } from "react";
import * as RSelect from "@radix-ui/react-select";
import { ChevronIcon, CheckIcon } from "../board/icons";

export interface SelectOption {
  value: string;
  label: string;
  // Leading glyph (e.g. the AgentMark), sized/tinted by the caller.
  icon?: ReactNode;
  disabled?: boolean;
}

interface Props {
  value: string;
  onChange: (v: string) => void;
  options: SelectOption[];
  placeholder?: string;
  // A trailing, disabled "Coming soon" section — labels only, never selectable —
  // so the picker leads with what you can actually run instead of a wall of
  // greyed-out rows that read as broken. Mirrors the old <optgroup> pattern.
  soon?: string[];
  ariaLabel?: string;
}

// Select is a thin, styled wrapper over Radix's Select primitives, keeping a
// plain value/onChange/options API at the call site. Radix gives us listbox
// roles, keyboard nav, and typeahead for free; the styling matches ContextMenu
// (design tokens, the app's "pop in" feel — see the keyframes in index.css).
export function Select({ value, onChange, options, placeholder, soon, ariaLabel }: Props) {
  // Controlled with a plain string (never undefined) so an initially-empty value
  // doesn't flip Radix from uncontrolled→controlled; an empty value matches no
  // item, so Select.Value falls back to the placeholder.
  return (
    <RSelect.Root value={value} onValueChange={onChange}>
      <RSelect.Trigger
        aria-label={ariaLabel}
        className="flex w-full items-center justify-between gap-2 rounded-lg border border-hairline bg-surface px-2.5 py-2 text-[13px] font-medium text-ink outline-none focus:border-ink/40 data-[placeholder]:font-normal data-[placeholder]:text-muted"
      >
        <RSelect.Value placeholder={placeholder} />
        <RSelect.Icon className="text-muted">
          <ChevronIcon />
        </RSelect.Icon>
      </RSelect.Trigger>
      <RSelect.Portal>
        <RSelect.Content
          position="popper"
          sideOffset={6}
          className="ultra-select-content z-[70] min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-xl border border-hairline bg-surface p-1 shadow-[0_16px_44px_-16px_rgba(23,23,26,0.45)]"
        >
          <RSelect.Viewport>
            {options.map((o) => (
              <Item key={o.value} value={o.value} disabled={o.disabled} icon={o.icon}>
                {o.label}
              </Item>
            ))}
            {soon && soon.length > 0 && (
              <RSelect.Group>
                <RSelect.Label className="px-2.5 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-[0.07em] text-muted">
                  Coming soon
                </RSelect.Label>
                {soon.map((label) => (
                  // A disabled item still needs a unique, non-empty value (Radix
                  // rejects value=""); it's never selectable, so the label serves.
                  <Item key={label} value={label} disabled>
                    {label}
                  </Item>
                ))}
              </RSelect.Group>
            )}
          </RSelect.Viewport>
        </RSelect.Content>
      </RSelect.Portal>
    </RSelect.Root>
  );
}

function Item({
  value,
  disabled,
  icon,
  children,
}: {
  value: string;
  disabled?: boolean;
  icon?: ReactNode;
  children: ReactNode;
}) {
  return (
    <RSelect.Item
      value={value}
      disabled={disabled}
      className="flex cursor-pointer select-none items-center gap-2 rounded-lg px-2.5 py-1.5 text-[13px] font-medium text-ink outline-none data-[highlighted]:bg-board data-[disabled]:cursor-not-allowed data-[disabled]:text-muted/50"
    >
      {icon && <span className="flex w-4 shrink-0 items-center justify-center">{icon}</span>}
      <RSelect.ItemText>{children}</RSelect.ItemText>
      <RSelect.ItemIndicator className="ml-auto text-ink">
        <CheckIcon />
      </RSelect.ItemIndicator>
    </RSelect.Item>
  );
}
