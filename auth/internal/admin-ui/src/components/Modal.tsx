import { useEffect, useRef } from "react";
import { ReactNode } from "react";

interface Props {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  actions?: ReactNode;
}

// modalStack tracks the open-modal LIFO order so Escape closes only the
// topmost. Without this, two stacked modals (e.g. IssueKey + IssuedSecret)
// would both close on a single Escape — which could lose the one-time
// subscription secret from the second modal.
const modalStack: Array<() => void> = [];

// Modal is a minimal centered-card dialog with backdrop. Closes on Escape
// (topmost only) and backdrop click (intentional click, not drag-out).
// No focus-trap library — adequate for an internal admin tool with a small
// surface area; revisit if accessibility audits flag it.
export function Modal({ open, title, onClose, children, actions }: Props) {
  useEffect(() => {
    if (!open) return;
    modalStack.push(onClose);
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      // Only the topmost modal's keydown reacts; the rest stay open.
      if (modalStack[modalStack.length - 1] !== onClose) return;
      onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      const i = modalStack.lastIndexOf(onClose);
      if (i >= 0) modalStack.splice(i, 1);
    };
  }, [open, onClose]);

  // Track mousedown target so a drag-to-select that *started* inside the
  // modal and *released* on the backdrop doesn't trigger close. Standard
  // click-outside semantics: down+up must both be on the backdrop.
  const downTarget = useRef<EventTarget | null>(null);

  if (!open) return null;

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        downTarget.current = e.target;
      }}
      onClick={(e) => {
        if (
          e.detail === 1 &&
          downTarget.current === e.currentTarget &&
          e.target === e.currentTarget
        ) {
          onClose();
        }
      }}
    >
      <div className="modal" role="dialog" aria-modal="true" aria-label={title}>
        <div className="modal-header">{title}</div>
        <div>{children}</div>
        {actions && <div className="modal-actions">{actions}</div>}
      </div>
    </div>
  );
}
