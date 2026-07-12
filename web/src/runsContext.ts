import { createContext, useContext } from "react";
import type { RunProgress } from "./api";

// RunsContext carries the board's live per-task flow progress (task id →
// RunProgress) so the deeply-nested TaskCard / FlowStepper can read a task's
// active step without prop-drilling the whole map through every board layout and
// column. Solo tasks are simply absent from the map.
export const RunsContext = createContext<Record<string, RunProgress>>({});

// useRun returns a task's live flow progress, or undefined for a solo task (no
// run) — in which case the stepper falls back to deriving a coarse step from
// status.
export function useRun(taskId: string): RunProgress | undefined {
  return useContext(RunsContext)[taskId];
}
