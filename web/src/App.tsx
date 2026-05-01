import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";

import { AuthProvider, RequireAdmin, RequireAuth, useAuth } from "./auth";
import { AuthLayout } from "./components/AuthLayout";
import { Layout } from "./components/Layout";
import { NotificationsProvider } from "./notifications";
import { AdminBackups } from "./pages/AdminBackups";
import { BatchCreate } from "./pages/BatchCreate";
import { BatchDetail } from "./pages/BatchDetail";
import { BatchEdit } from "./pages/BatchEdit";
import { Batches } from "./pages/Batches";
import { Calculators } from "./pages/Calculators";
import { Login } from "./pages/Login";
import { Me } from "./pages/Me";
import { Notifications } from "./pages/Notifications";
import { RecipeCreate } from "./pages/RecipeCreate";
import { RecipeDetail } from "./pages/RecipeDetail";
import { RecipeEdit } from "./pages/RecipeEdit";
import { Recipes } from "./pages/Recipes";
import { Register } from "./pages/Register";

// Already-authenticated users hitting /login or /register get bounced
// home — keeps the back button from leaving the user on a stale form.
function RedirectIfAuthed({ children }: { children: JSX.Element }) {
  const { state } = useAuth();
  if (state.status === "loading") return null;
  if (state.status === "authed") return <Navigate to="/" replace />;
  return children;
}

export function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <NotificationsProvider>
        <Routes>
          {/* Auth screens render bare — no header, no nav, just the card. */}
          <Route
            path="/login"
            element={
              <RedirectIfAuthed>
                <AuthLayout>
                  <Login />
                </AuthLayout>
              </RedirectIfAuthed>
            }
          />
          <Route
            path="/register"
            element={
              <RedirectIfAuthed>
                <AuthLayout>
                  <Register />
                </AuthLayout>
              </RedirectIfAuthed>
            }
          />
          {/* Everything else gets the chrome. */}
          <Route
            path="/"
            element={
              <Layout>
                <Recipes />
              </Layout>
            }
          />
          <Route
            path="/calculators"
            element={
              <Layout>
                <Calculators />
              </Layout>
            }
          />
          <Route
            path="/recipes/new"
            element={
              <RequireAuth>
                <Layout>
                  <RecipeCreate />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/recipes/:id"
            element={
              <Layout>
                <RecipeDetail />
              </Layout>
            }
          />
          <Route
            path="/recipes/:id/edit"
            element={
              <RequireAuth>
                <Layout>
                  <RecipeEdit />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/batches"
            element={
              <RequireAuth>
                <Layout>
                  <Batches />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/batches/new"
            element={
              <RequireAuth>
                <Layout>
                  <BatchCreate />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/batches/:id"
            element={
              <RequireAuth>
                <Layout>
                  <BatchDetail />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/batches/:id/edit"
            element={
              <RequireAuth>
                <Layout>
                  <BatchEdit />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/me"
            element={
              <RequireAuth>
                <Layout>
                  <Me />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/notifications"
            element={
              <RequireAuth>
                <Layout>
                  <Notifications />
                </Layout>
              </RequireAuth>
            }
          />
          {/* /admin lands on backups today; future admin sections add their
              own children. RequireAdmin gates on users.is_admin. */}
          <Route
            path="/admin"
            element={<Navigate to="/admin/backups" replace />}
          />
          <Route
            path="/admin/backups"
            element={
              <RequireAdmin>
                <Layout>
                  <AdminBackups />
                </Layout>
              </RequireAdmin>
            }
          />
          {/* Unknown SPA paths fall back home; the Go static handler
              already serves index.html for any non-/api/* path. */}
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
        </NotificationsProvider>
      </AuthProvider>
    </BrowserRouter>
  );
}
