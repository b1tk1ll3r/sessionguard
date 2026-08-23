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

    function checkAccessSession() {
        if (redirecting || !window.fetch) {
            return;
        }

        window.fetch(AUTH_BASE + '/status', {
            method: 'GET',
            credentials: 'same-origin',
            cache: 'no-store',
            headers: { 'Accept': 'application/json' }
        }).then(function (response) {
            if (response.status === 401 || response.status === 403) {
                // Navigating away also closes Guacamole WebSocket/tunnel
                // connections, so a revoked PocketID/SessionGuard access
                // session cannot keep an already-open browser client alive.
                redirect(loginURL());
            }
        }).catch(function () {
            // A transient auth-status outage must not destroy an active RDP
            // session. Traefik still fail-closes all new HTTP requests through
            // ForwardAuth; retry this browser-side check on the next interval.
        });
    }

    function watchForGuacamoleLogout() {
        function loggedOutModalPresent() {
            return document.querySelector('.logged-out-modal') !== null;
        }

        if (loggedOutModalPresent()) {
            redirect(logoutURL());
            return;
        }

        var observer = new MutationObserver(function () {
            if (loggedOutModalPresent()) {
                observer.disconnect();
                redirect(logoutURL());
            }
        });

        observer.observe(document.documentElement, {
            childList: true,
            subtree: true
        });
    }

    function start() {
        watchForGuacamoleLogout();
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
