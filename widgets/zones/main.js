// The NovaBar zone picker.
//
// A grid over the work area. Picking a cell tells the shell which zone the focused
// window should take; the shell lays the windows out around it.
//
// The grid is described by the shell rather than guessed here: how many cells it has
// and which one the window being moved would land in. The picker only has to draw it
// and report the answer.

const boot = window.NOVABAR || {};
const data = boot.data || {};

const grid = document.getElementById('grid');

/** The number of cells in the grid. */
let cells = typeof data.cells === 'number' ? data.cells : 4;

/** The cell the window being moved would take if nothing else was picked. */
let held = typeof data.held === 'number' ? data.held : 0;

/** The title of the window being moved, so the user can see what they are moving. */
const title = typeof data.title === 'string' ? data.title : '';

/**
 * Draw the grid.
 *
 * The cells are laid out in reading order, which is the order the shell numbers them
 * in: the cell a user sees in the top left is cell zero.
 */
function draw() {
  grid.style.gridTemplateColumns = `repeat(${cols()}, 1fr)`;
  grid.style.gridTemplateRows = `repeat(${rows()}, 1fr)`;
  grid.innerHTML = '';

  for (let i = 0; i < cells; i += 1) {
    const cell = document.createElement('div');
    cell.className = 'cell' + (i === held ? ' held' : '');
    cell.dataset.cell = String(i);

    const label = document.createElement('div');
    label.className = 'label';
    label.textContent = i === held ? title : '';
    cell.appendChild(label);

    cell.addEventListener('click', () => choose(i));
    grid.appendChild(cell);
  }

  const hint = document.createElement('div');
  hint.className = 'hint';
  hint.textContent = 'Pick a zone. Escape to cancel.';
  grid.appendChild(hint);
}

/** The number of columns in the grid, matching the shell's shape. */
function cols() {
  if (cells <= 1) return 1;
  if (cells === 2) return 2;
  if (cells === 3) return 3;
  if (cells === 4) return 2;
  if (cells <= 6) return 3;
  return 3;
}

/** The number of rows in the grid. */
function rows() {
  return Math.ceil(cells / cols());
}

/** Report the chosen cell to the shell and close the picker. */
function choose(index) {
  window.chrome.webview.postMessage(JSON.stringify({ type: 'zone', cell: index }));
}

// Escape cancels, which is the one thing a picker must be able to be dismissed
// without answering.
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') {
    window.chrome.webview.postMessage(JSON.stringify({ type: 'zone-cancel' }));
  }
});

window.addEventListener('message', (event) => {
  const data = event.data;
  if (!data) return;

  if (typeof data.cells === 'number') cells = data.cells;
  if (typeof data.held === 'number') held = data.held;
  draw();
});

// The shell describes the grid before the picker is shown, but a picker that
// arrives without a description should still draw something.
draw();
