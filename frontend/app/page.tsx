import { redirect } from "next/navigation";

// Root redirects straight to login. Update this once you have a real
// landing page or decide on a different default route.
export default function RootPage() {
  redirect("/login");
}
