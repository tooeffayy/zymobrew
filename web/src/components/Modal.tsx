import { ReactNode } from "react";
import {
  Dialog,
  Heading,
  Modal as AriaModal,
  ModalOverlay,
} from "react-aria-components";

// Thin wrapper over react-aria-components Modal/Dialog. Handles focus
// trap, Escape to close, click-outside dismiss, body scroll lock, and
// proper ARIA roles. Children get a `close()` callback so forms can
// dismiss after submit.
//
// We separate ModalOverlay from Modal because the overlay is the
// dimmed backdrop (full-viewport, fixed) and Modal is the dialog
// container — react-aria draws each so it can apply state attrs
// (`data-entering` etc.) for transitions on either layer independently.
export function Modal({
  isOpen,
  onOpenChange,
  title,
  children,
}: {
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  children: (close: () => void) => ReactNode;
}) {
  return (
    <ModalOverlay
      className="modal-overlay"
      isDismissable
      isOpen={isOpen}
      onOpenChange={onOpenChange}
    >
      <AriaModal className="modal">
        <Dialog className="modal-dialog">
          {({ close }) => (
            <>
              <div className="modal-header">
                <Heading slot="title" className="modal-title">{title}</Heading>
                <button
                  type="button"
                  className="link-button modal-close"
                  onClick={close}
                  aria-label="Close"
                >
                  ✕
                </button>
              </div>
              <div className="modal-body">
                {children(close)}
              </div>
            </>
          )}
        </Dialog>
      </AriaModal>
    </ModalOverlay>
  );
}
