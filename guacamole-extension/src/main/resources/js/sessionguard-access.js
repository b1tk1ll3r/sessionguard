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

    function recoverFromGuacamoleLogout() {
        if (redirecting) {
            return;
        }

        // Guacamole authentication tokens live in the selected webapp process.
        // A worker failover/restart can therefore cause Guacamole to enter its
        // logged-out state even though the upstream SessionGuard/PocketID
        // browser session is still valid. Never turn that condition into a
        // full IdP logout. Re-enter Guacamole and let header auth mint a fresh
        // Guacamole token instead.
        getAccessSessionStatus().then(function (response) {
            if (response.ok) {
                redirect(safeReturnURL());
            }
            else if (response.status === 401 || response.status === 403) {
                redirect(loginURL());
            }
        }).catch(function () {
            // Keep Guacamole's normal logged-out UI visible during a transient
            // SessionGuard outage instead of forcing any logout/login loop.
        });
    }

    function watchForGuacamoleLoggedOutState() {
        function loggedOutModalPresent() {
            return document.querySelector('.logged-out-modal') !== null;
        }

        if (loggedOutModalPresent()) {
            recoverFromGuacamoleLogout();
            return;
        }

        var observer = new MutationObserver(function () {
            if (loggedOutModalPresent()) {
                observer.disconnect();
                recoverFromGuacamoleLogout();
            }
        });

        observer.observe(document.documentElement, {
            childList: true,
            subtree: true
        });
    }

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
        watchForGuacamoleLoggedOutState();
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
