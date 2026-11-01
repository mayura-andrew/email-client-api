"use client";

import { createContext, ReactNode, useCallback, useContext, useState } from "react";
import { Icon } from "./Icon";

type Tone = "success" | "error" | "info";
type Toast = { id: number; tone: Tone; title: string; detail?: string };

const ToastContext = createContext<(tone: Tone, title: string, detail?: string) => void>(() => {});

export const useToast = () => useContext(ToastContext);

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const dismiss = useCallback((id: number) => setToasts(list => list.filter(toast => toast.id !== id)), []);
  const push = useCallback((tone: Tone, title: string, detail?: string) => {
    const id = Date.now() + Math.random();
    setToasts(list => [...list.slice(-3), { id, tone, title, detail }]);
    window.setTimeout(() => dismiss(id), tone === "error" ? 7000 : 4500);
  }, [dismiss]);
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="toasts" aria-live="polite">
        {toasts.map(toast => (
          <div key={toast.id} className={`toast toast-${toast.tone}`}>
            <span className="toast-icon"><Icon name={toast.tone === "success" ? "check" : toast.tone === "error" ? "alert" : "zap"} size={16} /></span>
            <div><strong>{toast.title}</strong>{toast.detail && <p>{toast.detail}</p>}</div>
            <button className="toast-close" onClick={() => dismiss(toast.id)} aria-label="Dismiss"><Icon name="x" size={14} /></button>
            <span className="toast-timer" style={{ animationDuration: toast.tone === "error" ? "7s" : "4.5s" }} />
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
