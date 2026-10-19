import { RouterProvider } from "react-router-dom";
import { router } from "./pages/Router";
import { World } from "rapido-ui/World";
import { ThemeSync } from "rapido-ui/ThemeSync";

function App() {
    // No padding: every Rapido page is a full-bleed shell that paints its own
    // background edge to edge.
    return (
        <main>
            {/* The 3D world lives here, above the router, so it is not rebuilt on every page change. */}
            <World />
            <ThemeSync />
            <RouterProvider router={router} />
        </main>
    );
}

export default App;
