// The NovaBar settings widget.
//
// Every control here is generated from config.schema. Nothing is hand-listed, so a setting
// added to the shell appears automatically and one removed disappears. The alternative is a
// settings page that quietly drifts from the config it edits, which is worse than having no
// settings page at all.

import { NovaBarSDK } from '/sdk/novabar.js';

const DISMISS = '__novabar_dismiss__';
const boot = window.NOVABAR;
const rows = document.getElementById('rows');
const toast = document.getElementById('toast');
const note = document.getElementById('note');

const sdk = new NovaBarSDK({
    port: boot?.port,
    token: boot?.token,
    widgetToken: boot?.widget_token,
});

/** Fields from the daemon, and their current values. */
let fields = [];
let values = {};

/** Paths the daemon gates on a working rescue path. */
let protectedPaths = new Set();

function showToast(message, bad = false) {
    toast.textContent = message;
    toast.classList.toggle('bad', bad);
    toast.classList.add('show');
    clearTimeout(showToast._t);
    showToast._t = setTimeout(() => toast.classList.remove('show'), 2600);
}

function showNote(message) {
    note.textContent = message;
    note.hidden = !message;
}

/** Push one change and refresh from the value the daemon settled on. */
async function apply(path, value) {
    try {
        const res = await sdk.call('config.set', { path, value });
        // The daemon revalidates, so the accepted value may not be what was sent. Taking
        // the daemon's copy is what keeps the display honest after a clamp.
        if (res && res.value) values = { ...values, ...res.value };
        render();
    } catch (err) {
        showToast(err.message || String(err), true);
        // Re-render to put the control back: the change was refused, so the displayed
        // value is now a lie.
        render();
    }
}

// Controls ------------------------------------------------------------------

function toggleControl(field) {
    const btn = document.createElement('button');
    const on = Boolean(values[field.path]);
    btn.className = 'switch' + (on ? ' on' : '');
    btn.setAttribute('role', 'switch');
    btn.setAttribute('aria-checked', String(on));
    btn.setAttribute('aria-label', field.label);
    btn.addEventListener('click', () => apply(field.path, !on));
    return btn;
}

function choiceControl(field) {
    const sel = document.createElement('select');
    sel.setAttribute('aria-label', field.label);
    for (const opt of field.options || []) {
        const o = document.createElement('option');
        o.value = opt;
        o.textContent = opt;
        if (opt === values[field.path]) o.selected = true;
        sel.appendChild(o);
    }
    sel.addEventListener('change', () => apply(field.path, sel.value));
    return sel;
}

function numberControl(field) {
    const input = document.createElement('input');
    input.type = 'number';
    if (field.min !== undefined && field.min !== 0) input.min = field.min;
    if (field.max) input.max = field.max;
    input.value = values[field.path] ?? '';
    input.setAttribute('aria-label', field.label);

    // Committed on blur or Enter rather than every keystroke, so typing "1" on the way to
    // "16" does not write 1 and retile the desktop in between.
    const commit = () => apply(field.path, Number(input.value));
    input.addEventListener('change', commit);
    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') { e.preventDefault(); commit(); }
    });
    return input;
}

/** Render a captured chord the way the config spells it, e.g. "Ctrl+Alt+R". */
function formatChord(e) {
    const parts = [];
    if (e.ctrlKey) parts.push('Ctrl');
    if (e.altKey) parts.push('Alt');
    if (e.shiftKey) parts.push('Shift');
    if (e.metaKey) parts.push('Win');

    const key = e.key;
    const named = { ' ': 'Space', ArrowUp: 'Up', ArrowDown: 'Down', ArrowLeft: 'Left', ArrowRight: 'Right' };
    if (named[key]) parts.push(named[key]);
    else if (key && key.length === 1 && /[a-z0-9]/i.test(key)) parts.push(key.toUpperCase());
    else if (key === 'Escape') parts.push('Escape');
    else if (key === 'Enter') parts.push('Enter');

    return parts.join('+');
}

function hotkeyControl(field) {
    const btn = document.createElement('button');
    btn.className = 'hotkey';
    btn.textContent = values[field.path] || '(unset)';
    btn.setAttribute('aria-label', `${field.label}, press to change`);

    btn.addEventListener('click', () => {
        btn.classList.add('capturing');
        btn.textContent = 'press keys…';

        const onKey = async (e) => {
            e.preventDefault();
            e.stopPropagation();

            if (e.key === 'Escape') {
                document.removeEventListener('keydown', onKey, true);
                btn.classList.remove('capturing');
                render();
                return;
            }

            const chord = formatChord(e);
            // Only a modifier alone is not a chord; wait for a real key.
            if (!chord || ['Control', 'Alt', 'Shift', 'Meta'].includes(e.key)) return;

            document.removeEventListener('keydown', onKey, true);
            btn.classList.remove('capturing');

            // Windows exposes no way to ask whether another application owns a
            // combination, so the daemon checks what it can: the shape of the chord and a
            // clash with our own bindings. Its rejection reason is shown either way.
            const check = await sdk.call('hotkey.check', { spec: chord }).catch(() => null);
            if (check && check.ok === false) {
                btn.classList.add('clash');
                showToast(check.reason || 'that combination cannot be used', true);
                setTimeout(() => btn.classList.remove('clash'), 1800);
                render();
                return;
            }
            await apply(field.path, chord);
        };

        document.addEventListener('keydown', onKey, true);
    });
    return btn;
}

function controlFor(field) {
    switch (field.type) {
        case 'bool': return toggleControl(field);
        case 'enum': return choiceControl(field);
        case 'int': return numberControl(field);
        case 'hotkey': return hotkeyControl(field);
        default: {
            const input = document.createElement('input');
            input.type = 'text';
            input.value = values[field.path] ?? '';
            input.addEventListener('change', () => apply(field.path, input.value));
            return input;
        }
    }
}

function render() {
    rows.replaceChildren();

    for (const field of fields) {
        const row = document.createElement('div');
        row.className = 'row' + (field.safety === 'protect' ? ' protected' : '');

        const text = document.createElement('div');
        text.className = 'text';

        const label = document.createElement('div');
        label.className = 'label';
        label.textContent = field.label;

        text.appendChild(label);

        if (field.help) {
            const help = document.createElement('div');
            help.className = 'help';
            help.textContent = field.help;
            text.appendChild(help);
        }

        // A size is meaningless with no bar, so it only appears when the bar is on. That
        // keeps the page short and avoids a control that does nothing.
        if (field.path.endsWith('.size')) {
            const edge = field.path.split('.')[1];
            const enabledField = fields.find((f) => f.path === `bars.${edge}.enabled`);
            if (enabledField && !values[enabledField.path]) continue;
        }

        const wrap = document.createElement('div');
        wrap.className = 'control';
        wrap.appendChild(controlFor(field));

        row.append(text, wrap);
        rows.appendChild(row);
    }
}

async function main() {
    if (!boot || !boot.hasBridge) {
        rows.textContent = 'No shell connection.';
        return;
    }

    try {
        await sdk.connect();
    } catch (err) {
        console.error('could not reach the daemon', err);
        rows.textContent = 'Could not reach the daemon.';
        return;
    }

    try {
        const schema = await sdk.call('config.schema');
        fields = schema.fields || [];
        values = schema.values || {};
        protectedPaths = new Set(schema.protected || []);

        const gated = fields.filter((f) => protectedPaths.has(f.path)).map((f) => f.label);
        if (gated.length) {
            showNote(`${gated.join(' and ')} can only be changed while the rescue hotkey works.`);
        }
        render();
    } catch (err) {
        console.error('could not load the settings schema', err);
        rows.textContent = `Could not load settings: ${err.message || err}`;
    }

    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
            // Tell the daemon to close us. Closing the window from the page directly would
            // leave the daemon holding a credential for a surface that no longer exists.
            if (boot.hasBridge) window.chrome.webview.postMessage(DISMISS);
        }
    });
}

main();