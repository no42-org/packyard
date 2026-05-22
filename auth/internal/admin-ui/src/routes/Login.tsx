import { useSearchParams } from "react-router-dom";

// Known error codes the OAuth callback may pass back via ?error=. Anything
// not in this set is rendered as a generic message so an attacker who
// crafts a `?error=PLEASE_CONTACT_supportscam@example.com` link cannot put
// arbitrary text on the login page. React already escapes children, but
// the social-engineering surface is real.
const KNOWN_ERROR_MESSAGES: Record<string, string> = {
  OPERATOR_NOT_ALLOWED: "Your email is not in the operator allowlist. Ask an admin to add you.",
  OPERATOR_DISABLED: "Your operator account is disabled. Ask an admin to re-enable it.",
  ORG_MEMBERSHIP_REQUIRED: "You are not a member of the configured GitHub organisation.",
  EMAIL_NOT_VERIFIED: "The OAuth provider returned an unverified email. Verify it and retry.",
  INVALID_OAUTH_STATE: "OAuth state mismatch — retry the login.",
  OAUTH_EXCHANGE_FAILED: "OAuth token exchange failed. Confirm app config and retry.",
  UNKNOWN_PROVIDER: "OAuth provider not configured. Contact an admin.",
  RATE_LIMITED: "Too many login attempts. Wait a minute and retry.",
  SESSION_CREATE_FAILED: "Server failed to create a session. Retry.",
  LOGIN_INIT_FAILED: "Server failed to start the OAuth flow. Retry.",
};

// Login is the provider chooser. The OAuth flow is initiated server-side:
// hitting /api/v1/auth/login/{provider} redirects to the IdP, which then
// hits the /callback endpoint. The SPA does not handle OAuth code/state
// directly; we just kick off the redirect.
export function Login() {
  const [params] = useSearchParams();
  const errorCode = params.get("error");
  const errorMessage = errorCode
    ? KNOWN_ERROR_MESSAGES[errorCode] ?? "Login failed. Retry, or ask an admin for help."
    : null;

  return (
    <div className="login-screen">
      <div className="login-card">
        <h1>Packyard Admin</h1>
        <p>Sign in with your operator identity provider.</p>

        {errorMessage && (
          <div className="error-banner" role="alert">
            {errorMessage}
          </div>
        )}

        <div className="login-providers">
          <a href="/api/v1/auth/login/github">Sign in with GitHub</a>
          <a href="/api/v1/auth/login/microsoft">Sign in with Microsoft</a>
        </div>
      </div>
    </div>
  );
}
