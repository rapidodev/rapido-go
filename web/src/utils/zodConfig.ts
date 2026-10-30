import { z } from "zod";

// The dashboard's content security policy refuses eval. Zod would otherwise try it
// once to find out, and the browser reports the attempt as a violation.
z.config({ jitless: true });
