import { useCallback, useEffect, useState } from "react";

// Board layout is a per-device preference (this is a local single-user tool), so
// it lives in localStorage rather than the daemon.
export type BoardLayout = "swimlanes" | "filter";

const KEY = "ultraflow.layout";

function read(): BoardLayout {
  const v = localStorage.getItem(KEY);
  return v === "swimlanes" || v === "filter" ? v : "filter";
}

// useLayout persists the swimlanes-vs-filter choice and keeps it in sync across
// tabs via the storage event.
export function useLayout(): [BoardLayout, (l: BoardLayout) => void] {
  const [layout, setLayout] = useState<BoardLayout>(read);

  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      if (e.key === KEY) setLayout(read());
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const set = useCallback((l: BoardLayout) => {
    setLayout(l);
    localStorage.setItem(KEY, l);
  }, []);

  return [layout, set];
}
