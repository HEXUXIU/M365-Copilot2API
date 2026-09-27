import { motion, AnimatePresence } from "motion/react";
import { useLayoutEffect, useRef, useState } from "react";

export function Toast({
  message,
  kind,
  onClose,
}: {
  message: string;
  kind: "success" | "error" | "info";
  onClose: () => void;
}) {
  const first = useRef(true);
  useLayoutEffect(() => {
    const id = setTimeout(onClose, 2600);
    return () => clearTimeout(id);
  }, [message, onClose]);
  void first;
  const bg = kind === "success" ? "var(--green)" : kind === "error" ? "var(--red)" : "var(--accent)";
  return (
    <motion.div
      initial={{ opacity: 0, y: 12, scale: 0.97 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 8, scale: 0.98 }}
      transition={{ duration: 0.24, ease: [0.22, 1, 0.36, 1] }}
      style={{
        position: "fixed",
        right: 24,
        bottom: 24,
        background: bg,
        color: "#fff",
        padding: "11px 18px",
        borderRadius: "var(--radius-sm)",
        fontWeight: 600,
        fontSize: 13,
        boxShadow: "var(--shadow)",
        zIndex: 200,
        maxWidth: 360,
      }}
      role="status"
    >
      {message}
    </motion.div>
  );
}

export function ToastHost({
  toasts,
  dismiss,
}: {
  toasts: { id: number; message: string; kind: "success" | "error" | "info" }[];
  dismiss: (id: number) => void;
}) {
  return (
    <AnimatePresence>
      {toasts.map((t) => (
        <Toast key={t.id} message={t.message} kind={t.kind} onClose={() => dismiss(t.id)} />
      ))}
    </AnimatePresence>
  );
}

export function useToasts() {
  const [toasts, setToasts] = useState<{ id: number; message: string; kind: "success" | "error" | "info" }[]>([]);
  const push = (message: string, kind: "success" | "error" | "info" = "info") => {
    const id = Date.now() + Math.random();
    setToasts((prev) => [...prev.slice(-2), { id, message, kind }]);
  };
  const dismiss = (id: number) => setToasts((prev) => prev.filter((t) => t.id !== id));
  return { toasts, push, dismiss };
}

export function Modal({
  title,
  children,
  onClose,
  width = 460,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
  width?: number;
}) {
  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.15 }}
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.4)",
        backdropFilter: "blur(4px)",
        display: "grid",
        placeItems: "center",
        zIndex: 100,
        padding: 16,
      }}
    >
      <motion.div
        initial={{ opacity: 0, scale: 0.96, y: 8 }}
        animate={{ opacity: 1, scale: 1, y: 0 }}
        exit={{ opacity: 0, scale: 0.97, y: 6 }}
        transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }}
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--surface-solid)",
          border: "1px solid var(--line)",
          borderRadius: "var(--radius)",
          boxShadow: "var(--shadow)",
          width: `min(${width}px, 100%)`,
          maxHeight: "calc(100vh - 48px)",
          overflow: "auto",
          padding: 22,
        }}
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <h3 style={{ margin: "0 0 10px", fontSize: 16 }}>{title}</h3>
        {children}
      </motion.div>
    </motion.div>
  );
}
