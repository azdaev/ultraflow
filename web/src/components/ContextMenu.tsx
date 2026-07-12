import { useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import {
  autoUpdate,
  flip,
  FloatingPortal,
  offset,
  shift,
  useDismiss,
  useFloating,
  useInteractions,
  useRole,
} from "@floating-ui/react";

// A right-click menu item. A separator carries no label; everything else is a
// clickable row. `danger` paints it rust (destructive), `disabled` greys it out.
export type MenuItem =
  | { separator: true }
  | {
      label: string;
      onSelect: () => void;
      danger?: boolean;
      disabled?: boolean;
    };

// useContextMenu wires an element's onContextMenu to a cursor-anchored floating
// menu. Floating UI positions a zero-size virtual reference at the pointer and
// flips/shifts the panel to stay on screen; we keep only the open state here so
// a card can spread `onContextMenu={menu.open}` and render <ContextMenu/> once.
export function useContextMenu() {
  const [open, setOpen] = useState(false);

  const floating = useFloating({
    open,
    onOpenChange: setOpen,
    placement: "right-start",
    strategy: "fixed",
    // Position via top/left, not transform: the panel is a motion.div whose
    // scale animation owns `transform`. If Floating UI also used transform to
    // place it, Motion would clobber the translate and the menu would snap to
    // the page's top-left corner. top/left leaves transform free for the pop.
    transform: false,
    middleware: [
      offset({ mainAxis: 6, crossAxis: 4 }),
      flip({ fallbackAxisSideDirection: "end", padding: 8 }),
      shift({ padding: 8 }),
    ],
    whileElementsMounted: autoUpdate,
  });

  const dismiss = useDismiss(floating.context);
  const role = useRole(floating.context, { role: "menu" });
  const interactions = useInteractions([dismiss, role]);

  function openMenu(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    const { clientX: x, clientY: y } = e;
    // Virtual reference: a zero-area rect sitting exactly under the cursor.
    floating.refs.setPositionReference({
      getBoundingClientRect: () => ({
        width: 0,
        height: 0,
        x,
        y,
        left: x,
        right: x,
        top: y,
        bottom: y,
      }),
    });
    setOpen(true);
  }

  return { open, setOpen, openMenu, floating, interactions };
}

type Menu = ReturnType<typeof useContextMenu>;

interface Props {
  menu: Menu;
  items: MenuItem[];
}

// ContextMenu renders the floating panel for a useContextMenu() handle. It pops
// in from the pointer (scale + fade, matching the app's spring feel), traps
// arrow-key navigation, and closes on select, Escape, or an outside click.
export function ContextMenu({ menu, items }: Props) {
  const { open, setOpen, floating, interactions } = menu;
  const rows = useRef<(HTMLButtonElement | null)[]>([]);

  // Grow the pop from the corner nearest the cursor, following any flip so the
  // menu never appears to unfold from the wrong edge near a screen boundary.
  const [side, align] = floating.placement.split("-");
  const originX = side === "left" ? "right" : side === "right" ? "left" : align === "end" ? "right" : "left";
  const originY = side === "top" ? "bottom" : side === "bottom" ? "top" : align === "end" ? "bottom" : "top";

  // Indices of the actionable (non-separator, enabled) rows, for arrow nav.
  const navigable = items.flatMap((it, i) =>
    "separator" in it || it.disabled ? [] : [i],
  );

  function move(current: number, dir: 1 | -1) {
    if (navigable.length === 0) return;
    const pos = navigable.indexOf(current);
    const next =
      pos === -1
        ? navigable[dir === 1 ? 0 : navigable.length - 1]
        : navigable[(pos + dir + navigable.length) % navigable.length];
    rows.current[next]?.focus();
  }

  function onKeyDown(e: React.KeyboardEvent, index: number) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      move(index, 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      move(index, -1);
    }
  }

  return (
    <FloatingPortal>
      <AnimatePresence>
        {open && (
          <motion.div
            ref={floating.refs.setFloating}
            style={{ ...floating.floatingStyles, transformOrigin: `${originY} ${originX}` }}
            {...interactions.getFloatingProps()}
            initial={{ opacity: 0, scale: 0.94 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.96, transition: { duration: 0.09, ease: [0.4, 0, 1, 1] } }}
            transition={{ type: "spring", stiffness: 460, damping: 32 }}
            className="z-[60] min-w-[184px] rounded-xl border border-hairline bg-surface p-1 shadow-[0_16px_44px_-16px_rgba(23,23,26,0.45)]"
          >
            {items.map((it, i) =>
              "separator" in it ? (
                <div key={i} className="my-1 h-px bg-hairline" />
              ) : (
                <button
                  key={i}
                  ref={(el) => {
                    rows.current[i] = el;
                  }}
                  role="menuitem"
                  disabled={it.disabled}
                  autoFocus={i === navigable[0]}
                  onKeyDown={(e) => onKeyDown(e, i)}
                  onClick={(e) => {
                    e.stopPropagation();
                    setOpen(false);
                    it.onSelect();
                  }}
                  className={`flex w-full items-center justify-between gap-4 rounded-lg px-2.5 py-1.5 text-left text-[13px] font-medium transition-colors ${
                    it.disabled
                      ? "cursor-not-allowed text-muted/50"
                      : it.danger
                        ? "text-rust hover:bg-rust-tint focus:bg-rust-tint"
                        : "text-ink hover:bg-board focus:bg-board"
                  } outline-none`}
                >
                  <span>{it.label}</span>
                </button>
              ),
            )}
          </motion.div>
        )}
      </AnimatePresence>
    </FloatingPortal>
  );
}
