import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Modal } from "../Modal";

describe("Modal", () => {
  it("renders a title via CardTitle and its children", () => {
    render(
      <Modal onClose={() => {}} title="Delete user">
        <p>Are you sure?</p>
      </Modal>
    );
    expect(screen.getByText("Delete user")).toBeInTheDocument();
    expect(screen.getByText("Are you sure?")).toBeInTheDocument();
  });

  it("renders no title element at all when title is omitted, for a fully custom header", () => {
    const { container } = render(
      <Modal onClose={() => {}}>
        <div data-testid="custom-header">Custom</div>
      </Modal>
    );
    expect(screen.getByTestId("custom-header")).toBeInTheDocument();
    // CardTitle always renders a div with this exact class combo - absent
    // here since no `title` prop was given.
    expect(container.querySelector(".text-base.font-semibold")).toBeNull();
  });

  it("closes when the overlay itself is clicked (outside the card)", () => {
    const onClose = vi.fn();
    render(
      <Modal onClose={onClose} title="t">
        <p>content</p>
      </Modal>
    );
    // The overlay is content's great-grandparent: content -> Card -> overlay.
    const overlay = screen.getByText("content").closest("div.fixed") as HTMLElement;
    fireEvent.mouseDown(overlay);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("does not close when a click starts inside the card", () => {
    const onClose = vi.fn();
    render(
      <Modal onClose={onClose} title="t">
        <p>content</p>
      </Modal>
    );
    fireEvent.mouseDown(screen.getByText("content"));
    expect(onClose).not.toHaveBeenCalled();
  });

  // On the card itself a caller's max-w-2xl never took effect: Tailwind sorts same-property
  // utilities by name, so it lost to the default max-w-sm and to the card's own max-w-full.
  it("sizes a wrapper from the caller's className, with no competing width beside it", () => {
    render(
      <Modal onClose={() => {}} className="max-w-2xl">
        <p>content</p>
      </Modal>
    );
    const wrapper = screen.getByText("content").closest(".max-w-2xl");
    expect(wrapper).not.toBeNull();
    expect(wrapper).not.toHaveClass("max-w-sm");
    expect(wrapper).not.toHaveClass("max-w-full");
  });

  it("falls back to the default width without a className", () => {
    render(
      <Modal onClose={() => {}}>
        <p>content</p>
      </Modal>
    );
    expect(screen.getByText("content").closest(".max-w-sm")).not.toBeNull();
  });
});
