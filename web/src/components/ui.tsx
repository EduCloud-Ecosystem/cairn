import { useId, useLayoutEffect, useRef, type ButtonHTMLAttributes, type ReactNode } from "react";

export type Notify = (msg: string, kind?: "ok" | "err") => void;

type Variant = "default" | "primary" | "danger" | "ghost";

export function Button({
  variant = "default",
  small,
  className,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; small?: boolean }) {
  const cls = [
    "btn",
    variant === "primary" ? "btn-primary" : "",
    variant === "danger" ? "btn-danger" : "",
    variant === "ghost" ? "btn-ghost" : "",
    small ? "btn-sm" : "",
    className ?? "",
  ]
    .filter(Boolean)
    .join(" ");
  return <button className={cls} {...rest} />;
}

export function Field({
  label,
  children,
  full,
}: {
  label: string;
  children: ReactNode;
  full?: boolean;
}) {
  return (
    <label className={"field" + (full ? " span-2" : "")}>
      <span>{label}</span>
      {children}
    </label>
  );
}

// Maps a server status string to a chip style.
const CHIP: Record<string, string> = {
  active: "chip-ok",
  succeeded: "chip-ok",
  invited: "chip-warn",
  provisioning: "chip-info",
  running: "chip-info",
  locked: "chip-slate",
  removed: "chip-muted",
  failed: "chip-danger",
  error: "chip-danger",
};

export function StatusChip({ status }: { status: string }) {
  const cls = CHIP[status] ?? "chip-muted";
  return <span className={`chip ${cls}`}>{status || "unknown"}</span>;
}

export function Modal({
  title,
  subtitle,
  onClose,
  children,
}: {
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const subtitleId = useId();
  useLayoutEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, []);

  return (
    <dialog
      ref={dialog}
      className="modal"
      tabIndex={-1}
      aria-labelledby={titleId}
      onKeyDown={(event) => {
        if (event.key !== "Tab") return;
        const controls = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(
          'button, [href], input, select, textarea, summary, [tabindex]',
        )).filter((element) => element.tabIndex >= 0 &&
          !element.matches(':disabled, [hidden]') && element.getClientRects().length > 0);
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (!first) { event.preventDefault(); event.currentTarget.focus(); }
        else if (event.shiftKey && (document.activeElement === first || document.activeElement === event.currentTarget)) {
          event.preventDefault(); last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault(); first.focus();
        }
      }}
      aria-describedby={subtitle ? subtitleId : undefined}
      onCancel={(event) => { event.preventDefault(); onClose(); }}
      onClick={(event) => {
        if (event.target !== event.currentTarget) return;
        const box = event.currentTarget.getBoundingClientRect();
        if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) onClose();
      }}
    >
      <div>
        <h2 id={titleId}>{title}</h2>
        {subtitle && <p id={subtitleId} className="modal-sub">{subtitle}</p>}
        {children}
      </div>
    </dialog>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}
