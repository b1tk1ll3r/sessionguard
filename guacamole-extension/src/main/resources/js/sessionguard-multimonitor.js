/*
 * SessionGuard multi-monitor ("span") helper for Apache Guacamole 1.6.
 *
 * Guacamole 1.6 has no native RDP multi-monitor support. This helper opens the
 * current connection in one browser window that covers several local screens.
 * With the connection parameter resize-method=display-update, Guacamole then
 * resizes the remote desktop to the window, comparable to "mstsc /span".
 *
 * Whether the button is offered is decided per connection and per user by the
 * SessionGuard Master (/_sessionguard/auth/display-policy). Chromium-based
 * browsers place the window automatically via the Window Management API; other
 * browsers get a resizable window which the user stretches manually.
 *
 * Plain JavaScript without a build step, loaded from the extension JAR.
 */
(function () {
    'use strict';

    var AUTH_BASE = '/_sessionguard/auth';
    var SPAN_PREFIX = 'sessionguard-span';
    var MIN_SIZE = 200;
    var policyCache = {};
    var activeRoute = null;
    var bar = null;
    var spanTarget = null;

    // Guacamole client URLs look like #/client/<id>, where <id> is
    // base64(connectionIdentifier + "\0" + "c" + "\0" + dataSource).
    function parseClientRoute() {
        var match = /^#\/client\/([^/?]+)/.exec(window.location.hash || '');
        if (!match) {
            return null;
        }
        var raw = match[1];
        try { raw = decodeURIComponent(raw); } catch (e) { return null; }
        var b64 = raw.replace(/-/g, '+').replace(/_/g, '/');
        while (b64.length % 4) {
            b64 += '=';
        }
        var parts;
        try { parts = window.atob(b64).split('\u0000'); } catch (e) { return null; }
        if (parts.length < 3 || parts[1] !== 'c' || !parts[0]) {
            return null;
        }
        return { key: match[1], id: parts[0], dataSource: parts[2] };
    }

    function fetchPolicy(route) {
        if (policyCache[route.key]) {
            return policyCache[route.key];
        }
        var url = AUTH_BASE + '/display-policy?connection_id=' + encodeURIComponent(route.id)
            + '&connection_name=' + encodeURIComponent(document.title || '');
        var request = window.fetch(url, {
            method: 'GET', credentials: 'same-origin', cache: 'no-store',
            headers: { 'Accept': 'application/json' }
        }).then(function (response) {
            if (!response.ok) {
                throw new Error('display-policy HTTP ' + response.status);
            }
            return response.json();
        });
        policyCache[route.key] = request;
        request.catch(function () { delete policyCache[route.key]; });
        return request;
    }

    // ---------------------------------------------------------------- UI --

    function toast(text) {
        var el = document.createElement('div');
        el.className = 'sg-mm-toast';
        el.textContent = text;
        document.body.appendChild(el);
        window.setTimeout(function () { el.remove(); }, 7000);
    }

    function removeBar() {
        if (bar) {
            bar.remove();
            bar = null;
        }
    }

    function button(label, title, onClick) {
        var b = document.createElement('button');
        b.type = 'button';
        b.textContent = label;
        b.title = title;
        b.addEventListener('click', function (event) {
            event.preventDefault();
            event.stopPropagation();
            onClick();
        });
        // Keep pointer/keyboard events away from Guacamole's input handlers.
        ['mousedown', 'mouseup', 'pointerdown', 'pointerup', 'keydown', 'keyup'].forEach(function (type) {
            b.addEventListener(type, function (event) { event.stopPropagation(); });
        });
        return b;
    }

    function showBar(route, policy) {
        removeBar();
        bar = document.createElement('div');
        bar.className = 'sg-mm-bar';
        var handle = document.createElement('span');
        handle.className = 'sg-mm-handle';
        handle.textContent = '⧉';
        bar.appendChild(handle);
        if (isSpanWindow()) {
            bar.appendChild(button('Erneut über Monitore legen', 'Fenster wieder über alle freigegebenen Monitore legen', function () {
                fitSpanWindow(true);
            }));
            bar.appendChild(button('Ein Monitor', 'Fenster auf den aktuellen Monitor verkleinern', singleScreen));
        }
        else {
            bar.appendChild(button('Multi-Monitor (' + policy.max_monitors + ')',
                'Sitzung in einem Fenster über mehrere Monitore öffnen', function () { startSpan(route, policy); }));
        }
        document.body.appendChild(bar);
    }

    // ----------------------------------------------------------- geometry --

    function overlap(a1, a2, b1, b2) {
        return Math.max(0, Math.min(a2, b2) - Math.max(a1, b1));
    }

    // Returns the largest rectangle covering a row (or column) of up to `max`
    // adjacent screens that includes the current screen and stays within the
    // guacd display-update limit.
    function computeSpan(details, policy) {
        var screens = Array.prototype.slice.call(details.screens || []);
        if (screens.length < 2) {
            return null;
        }
        var current = details.currentScreen || screens[0];
        var first = screens[0], second = screens[1];
        var vOverlap = overlap(first.availTop, first.availTop + first.availHeight, second.availTop, second.availTop + second.availHeight);
        var horizontal = vOverlap >= Math.min(first.availHeight, second.availHeight) / 2;
        screens.sort(function (a, b) { return horizontal ? a.availLeft - b.availLeft : a.availTop - b.availTop; });

        var max = Math.max(2, Math.min(policy.max_monitors || 2, screens.length));
        var idx = Math.max(0, screens.indexOf(current));
        var start = Math.max(0, Math.min(idx - Math.floor((max - 1) / 2), screens.length - max));
        var chosen = screens.slice(start, start + max);

        function deviceSize(list) {
            return list.reduce(function (sum, s) {
                return sum + (horizontal ? s.availWidth : s.availHeight) * (s.devicePixelRatio || 1);
            }, 0);
        }
        var limit = horizontal ? (policy.max_width || 8192) : (policy.max_height || 8192);
        while (chosen.length > 1 && deviceSize(chosen) > limit) {
            if (chosen[chosen.length - 1] !== current) { chosen.pop(); } else { chosen.shift(); }
        }
        if (chosen.length < 2) {
            return null;
        }

        var rect = {
            left: Math.min.apply(null, chosen.map(function (s) { return s.availLeft; })),
            top: Math.min.apply(null, chosen.map(function (s) { return s.availTop; })),
            right: Math.max.apply(null, chosen.map(function (s) { return s.availLeft + s.availWidth; })),
            bottom: Math.max.apply(null, chosen.map(function (s) { return s.availTop + s.availHeight; }))
        };
        // Use the common band so that the window is visible on every screen.
        if (horizontal) {
            rect.top = Math.max.apply(null, chosen.map(function (s) { return s.availTop; }));
            rect.bottom = Math.min.apply(null, chosen.map(function (s) { return s.availTop + s.availHeight; }));
        }
        else {
            rect.left = Math.max.apply(null, chosen.map(function (s) { return s.availLeft; }));
            rect.right = Math.min.apply(null, chosen.map(function (s) { return s.availLeft + s.availWidth; }));
        }
        var out = { left: rect.left, top: rect.top, width: rect.right - rect.left, height: rect.bottom - rect.top, count: chosen.length };
        return out.width >= MIN_SIZE && out.height >= MIN_SIZE ? out : null;
    }

    // ------------------------------------------------------------- launch --

    function startSpan(route, policy) {
        if (typeof window.getScreenDetails !== 'function') {
            launch(null, 'Dieser Browser kann Fenster nicht automatisch über mehrere Monitore legen. '
                + 'Ziehen Sie das neue Fenster bitte manuell über Ihre Monitore - die Sitzung passt sich an.');
            return;
        }
        window.getScreenDetails().then(function (details) {
            var rect = computeSpan(details, policy);
            if (!rect) {
                toast('Es wurde nur ein nutzbarer Monitor erkannt.');
                return;
            }
            launch(rect, null);
        }, function () {
            launch(null, 'Der Zugriff auf die Bildschirmanordnung wurde nicht erlaubt. '
                + 'Ziehen Sie das neue Fenster bitte manuell über Ihre Monitore.');
        });
    }

    function launch(rect, hint) {
        var geometry = rect || { left: window.screenX, top: window.screenY, width: window.outerWidth, height: window.outerHeight };
        var name = SPAN_PREFIX + ':' + [geometry.left, geometry.top, geometry.width, geometry.height].join(',') + (hint ? ':manual' : '');
        var features = 'popup=yes,left=' + geometry.left + ',top=' + geometry.top
            + ',width=' + geometry.width + ',height=' + geometry.height;
        var win = window.open(window.location.href, name, features);
        if (!win) {
            toast('Das Multi-Monitor-Fenster wurde vom Popup-Blocker verhindert. Bitte Popups für diese Seite erlauben und erneut klicken.');
            return;
        }
        try {
            win.moveTo(geometry.left, geometry.top);
            win.resizeTo(geometry.width, geometry.height);
        } catch (e) { /* the span window retries itself */ }

        // Release the connection in this tab. Guacamole keeps clients of the
        // home screen connected, so only a reload reliably closes the tunnel
        // and lets the RDS session move to the new window without ping-pong.
        window.setTimeout(function () {
            window.location.hash = '#/';
            window.location.reload();
        }, 50);
    }

    // -------------------------------------------------------- span window --

    function isSpanWindow() {
        return typeof window.name === 'string' && window.name.indexOf(SPAN_PREFIX + ':') === 0;
    }

    function parseSpanTarget() {
        var parts = window.name.split(':');
        var nums = (parts[1] || '').split(',').map(Number);
        if (nums.length !== 4 || nums.some(isNaN)) {
            return null;
        }
        return { left: nums[0], top: nums[1], width: nums[2], height: nums[3], manual: parts[2] === 'manual' };
    }

    function fitSpanWindow(force) {
        if (!spanTarget || (spanTarget.manual && !force)) {
            return;
        }
        var tries = 0;
        (function fit() {
            var ok = Math.abs(window.outerWidth - spanTarget.width) <= 16 && Math.abs(window.outerHeight - spanTarget.height) <= 16;
            if (ok) {
                return;
            }
            try {
                window.moveTo(spanTarget.left, spanTarget.top);
                window.resizeTo(spanTarget.width, spanTarget.height);
            } catch (e) { /* ignored */ }
            if (++tries < 4) {
                window.setTimeout(fit, 400);
            }
            else if (window.outerWidth < spanTarget.width - 16) {
                toast('Der Browser hat das Fenster auf einen Monitor begrenzt. Ziehen Sie es bitte manuell über alle Monitore - die Sitzung passt sich automatisch an.');
            }
        }());
    }

    function singleScreen() {
        var s = window.screen;
        try {
            window.moveTo(s.availLeft || 0, s.availTop || 0);
            window.resizeTo(s.availWidth, s.availHeight);
        } catch (e) { /* ignored */ }
    }

    function initSpanWindow() {
        spanTarget = parseSpanTarget();
        if (!spanTarget) {
            return;
        }
        document.documentElement.classList.add('sg-span-window');
        if (spanTarget.manual) {
            toast('Ziehen Sie dieses Fenster bitte über Ihre Monitore - die Remote-Sitzung passt ihre Auflösung automatisch an.');
        }
        window.setTimeout(function () { fitSpanWindow(false); }, 300);
    }

    // -------------------------------------------------------------- route --

    function onRouteChange() {
        var route = parseClientRoute();
        if (!route) {
            activeRoute = null;
            removeBar();
            return;
        }
        if (activeRoute && activeRoute.key === route.key && bar) {
            return;
        }
        activeRoute = route;
        // Give Guacamole a moment to set the page title (= connection name).
        window.setTimeout(function () {
            if (!activeRoute || activeRoute.key !== route.key) {
                return;
            }
            fetchPolicy(route).then(function (policy) {
                if (activeRoute && activeRoute.key === route.key && policy && policy.multi_monitor) {
                    showBar(route, policy);
                }
                else if (activeRoute && activeRoute.key === route.key) {
                    removeBar();
                }
            }).catch(function () { removeBar(); });
        }, 1200);
    }

    function start() {
        if (!window.fetch || !window.atob) {
            return;
        }
        if (isSpanWindow()) {
            initSpanWindow();
        }
        window.addEventListener('hashchange', onRouteChange);
        onRouteChange();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', start, { once: true });
    }
    else {
        start();
    }
}());
