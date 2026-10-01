import { motion, AnimatePresence } from "motion/react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { X } from "lucide-react";
import { t } from "./i18n";

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
      initial={{ opacity: 0, transform: "translateY(12px) scale(0.97)" }}
      animate={{ opacity: 1, transform: "translateY(0px) scale(1)" }}
      exit={{ opacity: 0, transform: "translateY(8px) scale(0.98)" }}
      transition={{ duration: 0.2, ease: [0.23, 1, 0.32, 1] }}
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
  subtitle,
  children,
  onClose,
  width = 480,
}: {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
  onClose: () => void;
  width?: number;
}) {
  const boxRef = useRef<HTMLDivElement>(null);
  // Callers pass a fresh arrow on every render, so the effect must not depend on
  // onClose; keep it in a ref and run the setup exactly once.
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    boxRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onCloseRef.current();
        return;
      }
      if (e.key !== "Tab" || !boxRef.current) return;
      const focusables = boxRef.current.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
      );
      if (focusables.length === 0) return;
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      const onContainer = document.activeElement === boxRef.current;
      if (e.shiftKey && (document.activeElement === first || onContainer)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      previous?.focus?.();
    };
  }, []);

  return (
    <motion.div
      className="modal-overlay"
      style={{ display: "flex" }}
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.15, ease: [0.23, 1, 0.32, 1] }}
      onClick={onClose}
    >
      <motion.div
        ref={boxRef}
        tabIndex={-1}
        className="modal"
        style={{ width: `min(${width}px, calc(100vw - 32px))` }}
        initial={{ opacity: 0, transform: "scale(0.96) translateY(8px)" }}
        animate={{ opacity: 1, transform: "scale(1) translateY(0)" }}
        exit={{ opacity: 0, transform: "scale(0.97) translateY(6px)" }}
        transition={{ duration: 0.22, ease: [0.23, 1, 0.32, 1] }}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <div className="modal-head">
          <div>
            <h3>{title}</h3>
            {subtitle ? <div className="subtitle">{subtitle}</div> : null}
          </div>
          <button className="modal-close" onClick={onClose} aria-label={t("Close")}><X size={16} /></button>
        </div>
        {children}
      </motion.div>
    </motion.div>
  );
}
