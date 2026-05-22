import { NavLink, Navigate, Route, Routes } from "react-router-dom";

import { Accounts } from "./routes/Accounts";
import { AccountDetail } from "./routes/AccountDetail";
import { Audit } from "./routes/Audit";
import { Components } from "./routes/Components";
import { Login } from "./routes/Login";
import { Operators } from "./routes/Operators";
import { useCurrentOperator } from "./api/session";

// App is the routed shell. The top navbar shows links to every section the
// current operator can use; admin-only sections (Operators) are hidden for
// readonly operators. Authentication state is read once at mount via
// /api/v1/auth/whoami; a 401 redirects to /login.
export function App() {
  const { data: me, isLoading, error } = useCurrentOperator();

  if (isLoading) {
    return <div className="loading">Loading…</div>;
  }

  // Unauthenticated → only /login is reachable.
  const isLoggedOut = !!error || !me;
  if (isLoggedOut) {
    return (
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  // Compare role case-insensitively so a server that ever returns "Admin"
  // (case drift) doesn't silently drop the operator's admin-only UI.
  const isAdmin = me.role.toLowerCase() === "admin";

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">Packyard</div>
        <nav className="nav">
          <NavLink to="/accounts">Accounts</NavLink>
          <NavLink to="/components">Components</NavLink>
          {isAdmin && <NavLink to="/operators">Operators</NavLink>}
          <NavLink to="/audit">Audit</NavLink>
        </nav>
        <div className="me">
          <span>{me.email}</span>
          <span className="role-pill">{me.role}</span>
          <LogoutButton />
        </div>
      </header>

      <main className="content">
        <Routes>
          <Route path="/" element={<Navigate to="/accounts" replace />} />
          <Route path="/login" element={<Navigate to="/accounts" replace />} />
          <Route path="/accounts" element={<Accounts />} />
          <Route path="/accounts/:id" element={<AccountDetail />} />
          <Route path="/components" element={<Components />} />
          {isAdmin && <Route path="/operators" element={<Operators />} />}
          <Route path="/audit" element={<Audit />} />
          <Route path="*" element={<Navigate to="/accounts" replace />} />
        </Routes>
      </main>
    </div>
  );
}

function LogoutButton() {
  return (
    <button
      type="button"
      className="btn-link"
      onClick={async () => {
        try {
          await fetch("/api/v1/auth/logout", {
            method: "POST",
            credentials: "include",
          });
        } finally {
          window.location.href = "/admin/login";
        }
      }}
    >
      Log out
    </button>
  );
}
