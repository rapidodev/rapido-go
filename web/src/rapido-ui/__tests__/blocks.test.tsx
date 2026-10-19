import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { AreaChart, Sparkline, chartDomain, chartRuns } from "../AreaChart";
import { DataTable } from "../DataTable";
import { EmptyState } from "../EmptyState";
import { StatusPill } from "../StatusPill";
import { Timeline } from "../Timeline";

describe("Timeline", () => {
  it("lists events in the given order with their time, title and details", () => {
    render(
      <Timeline
        aria-label="Outages"
        items={[
          { id: 1, time: "10:02", title: "Path down", description: "no reply", tone: "red", live: true },
          { id: 2, time: "09:40", title: "Recovered", tone: "green", meta: <span>3m12s</span> },
        ]}
      />
    );
    const list = screen.getByRole("list", { name: "Outages" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("Path down");
    expect(items[0]).toHaveTextContent("10:02");
    expect(items[0]).toHaveTextContent("no reply");
    expect(items[1]).toHaveTextContent("3m12s");
  });

  it("shows the empty slot instead of an empty list", () => {
    render(<Timeline items={[]} empty={<p>Nothing happened</p>} />);
    expect(screen.getByText("Nothing happened")).toBeInTheDocument();
    expect(screen.queryByRole("list")).toBeNull();
  });
});

describe("StatusPill", () => {
  it("is a toned label with a dot", () => {
    const { container } = render(<StatusPill tone="green" live>Up</StatusPill>);
    expect(screen.getByText("Up")).toHaveClass("text-emerald-300");
    expect(container.querySelector(".animate-ping")).not.toBeNull();
  });

  it("does not pulse unless live", () => {
    const { container } = render(<StatusPill tone="red">Down</StatusPill>);
    expect(container.querySelector(".animate-ping")).toBeNull();
  });
});

describe("EmptyState", () => {
  it("shows the title, the reason and the action", () => {
    render(<EmptyState title="Nothing here yet" description="Add one to start" action={<button>Add</button>} />);
    expect(screen.getByText("Nothing here yet")).toBeInTheDocument();
    expect(screen.getByText("Add one to start")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add" })).toBeInTheDocument();
  });
});

describe("AreaChart", () => {
  it("splits a series into runs at its gaps", () => {
    expect(chartRuns([1, 2, null, 4, undefined, 6, 7])).toEqual([
      [{ i: 0, v: 1 }, { i: 1, v: 2 }],
      [{ i: 3, v: 4 }],
      [{ i: 5, v: 6 }, { i: 6, v: 7 }],
    ]);
  });

  it("puts non-negative data on a zero baseline unless a min is given", () => {
    expect(chartDomain([5, 9])).toEqual({ lo: 0, hi: 9 });
    expect(chartDomain([5, 9], 4)).toEqual({ lo: 4, hi: 9 });
    expect(chartDomain([-2, 3])).toEqual({ lo: -2, hi: 3 });
    expect(chartDomain([])).toEqual({ lo: 0, hi: 1 });
    expect(chartDomain([3, 3], 3, 3)).toEqual({ lo: 3, hi: 4 });
  });

  it("describes its latest, lowest and highest value for assistive tech", () => {
    render(<AreaChart values={[10, 40, 25]} />);
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "25 (min 10, max 40)");
  });

  it("says there is no data for an empty or all-gap series", () => {
    render(<AreaChart values={[null, undefined]} />);
    expect(screen.getByRole("img")).toHaveAttribute("aria-label", "no data");
  });

  it("draws one line per run and a dashed rule per threshold band", () => {
    const { container } = render(
      <AreaChart
        values={[10, 20, null, 30, 90]}
        bands={[
          { from: 50, color: "#fbbf24" },
          { from: 80, color: "#fb7185" },
        ]}
        area={false}
      />
    );
    expect(container.querySelectorAll("svg path")).toHaveLength(2);
    expect(container.querySelectorAll("svg line")).toHaveLength(2);
  });

  it("hides the threshold rules and the range labels in the sparkline variant", () => {
    const { container } = render(<Sparkline values={[1, 2, 3]} bands={[{ from: 2, color: "red" }]} />);
    expect(container.querySelectorAll("svg line")).toHaveLength(0);
    expect(container.textContent).toBe("");
  });
});

type Row = { id: number; name: string };
const rows: Row[] = [
  { id: 1, name: "alpha" },
  { id: 2, name: "beta" },
  { id: 3, name: "gamma" },
];
const columns = [{ key: "name", header: "Name", cell: (r: Row) => r.name }];

const Harness = ({ onBulk }: { onBulk?: (r: Row[]) => void }) => {
  const [sel, setSel] = useState<Set<import("react").Key>>(new Set());
  return (
    <DataTable
      columns={columns}
      rows={rows}
      rowKey={(r) => r.id}
      selectable
      selected={sel}
      onSelectionChange={setSel}
      bulkActions={(picked) => <button onClick={() => onBulk?.(picked)}>Delete {picked.length}</button>}
    />
  );
};

describe("DataTable", () => {
  it("renders a header and one row per item", () => {
    render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} />);
    expect(screen.getByRole("columnheader", { name: "Name" })).toBeInTheDocument();
    expect(screen.getAllByRole("row")).toHaveLength(4);
  });

  it("pins the header with sticky cells", () => {
    render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} />);
    expect(screen.getByRole("columnheader", { name: "Name" })).toHaveClass("sticky", "top-0");
  });

  it("selects rows, shows the bulk bar with the picked rows, and clears", () => {
    const onBulk = vi.fn();
    render(<Harness onBulk={onBulk} />);
    expect(screen.queryByRole("status")).toBeNull();
    const boxes = screen.getAllByRole("checkbox", { name: "Select row" });
    fireEvent.click(boxes[0]);
    fireEvent.click(boxes[2]);
    expect(screen.getByRole("status")).toHaveTextContent("Selected: 2");
    fireEvent.click(screen.getByRole("button", { name: "Delete 2" }));
    expect(onBulk).toHaveBeenCalledWith([rows[0], rows[2]]);
    fireEvent.click(screen.getByRole("button", { name: "Clear selection" }));
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("select-all toggles every row and goes indeterminate for a partial pick", () => {
    render(<Harness />);
    const all = screen.getByRole("checkbox", { name: "Select all rows" }) as HTMLInputElement;
    fireEvent.click(screen.getAllByRole("checkbox", { name: "Select row" })[1]);
    expect(all.indeterminate).toBe(true);
    fireEvent.click(all);
    expect(all.checked).toBe(true);
    expect(screen.getByRole("status")).toHaveTextContent("Selected: 3");
    fireEvent.click(all);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("shows the empty slot, and skeleton rows while loading", () => {
    const { rerender } = render(<DataTable columns={columns} rows={[]} rowKey={(r: Row) => r.id} empty="No rows" />);
    expect(screen.getByText("No rows")).toBeInTheDocument();
    rerender(<DataTable columns={columns} rows={[]} rowKey={(r: Row) => r.id} empty="No rows" loading />);
    expect(screen.queryByText("No rows")).toBeNull();
  });
});
