import { notFound } from "next/navigation";

// Unknown URLs land here so the 404 renders inside the app shell (sidebar and
// navigation) instead of the bare root not-found page.
export default function CatchAll() {
  notFound();
}
