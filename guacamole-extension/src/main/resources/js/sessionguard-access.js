/*
 * SessionGuard access-session integration for Apache Guacamole.
 *
 * There is intentionally no npm/build step here. Guacamole loads this file
 * directly from the SessionGuard extension JAR.
 */
(function () {
    'use strict';

    var AUTH_BASE = '/_sessionguard/auth';
    var STATUS_INTERVAL_MS = 30000;
    var redirecting = false;

    function safeReturnURL() {
        return window.location.origin + '/';
    }

    function loginURL() {
        return AUTH_BASE + '/login?return=' + encodeURIComponent(safeReturnURL());
    }

    function logoutURL() {
        return AUTH_BASE + '/logout?return=' + encodeURIComponent(safeReturnURL());
    }

    function redirect(url) {
        if (redirecting) {
            return;
        }
        redirecting = true;
        window.location.replace(url);
    }

    function getAccessSessionStatus() {
        if (!window.fetch) {
            return Promise.reject(new Error('fetch unavailable'));
        }

        return window.fetch(AUTH_BASE + '/status', {
            method: 'GET',
            credentials: 'same-origin',
            cache: 'no-store',
            headers: { 'Accept': 'application/json' }
        });
    }

    function checkAccessSession() {
        if (redirecting) {
            return;
        }

        getAccessSessionStatus().then(function (response) {
            if (response.status === 401 || response.status === 403) {
                // The SessionGuard/PocketID access session itself is no longer
                // valid. Start a fresh OIDC flow, but do not treat this as an
                // explicit logout request.
                redirect(loginURL());
            }
        }).catch(function () {
            // A transient auth-status outage must not destroy an active RDP
            // session. ForwardAuth still fail-closes new requests and this
            // browser-side check will retry on the next interval.
        });
    }

    // Guacamole owns its own authentication state. Do not automatically
    // reload the SPA merely because Guacamole is in its logged-out state.
    // With header authentication, Guacamole's built-in "Login again" action
    // can re-authenticate against the next request. Automatic reload here
    // creates a / -> /#/ -> / loop while the SessionGuard access session is
    // still perfectly valid.

    function watchForExplicitLogout() {
        // Guacamole assigns the CSS class "logout" to its explicit logout
        // actions. Observe the user's click rather than the generic
        // .logged-out-modal state: the latter can also result from worker
        // failover, token loss, expiry or other non-user-initiated events.
        document.addEventListener('click', function (event) {
            var element = event.target;
            if (!element || typeof element.closest !== 'function') {
                return;
            }

            if (element.closest('.logout')) {
                // Let Guacamole's own click handler invalidate its local auth
                // token first, then perform SessionGuard/PocketID logout.
                window.setTimeout(function () {
                    redirect(logoutURL());
                }, 150);
            }
        }, false);
    }

    function start() {
        watchForExplicitLogout();
        checkAccessSession();
        window.setInterval(checkAccessSession, STATUS_INTERVAL_MS);
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', start, { once: true });
    }
    else {
        start();
    }
}());
