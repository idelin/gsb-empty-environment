import { TreeController, type Row } from '../src/controller.ts';
import { fixtureTree, remoteChildren, fixtureLoader } from '../fixtures/tree.fixture.ts';
import { fixtureGlyphs } from '../fixtures/glyphs.fixture.ts';

const controller = new TreeController(fixtureTree, {
  loader: fixtureLoader({ remote: remoteChildren }),
});

function render(title: string): void {
  console.log(`\n== ${title} ==`);
  for (const r of controller.rows()) console.log(formatRow(r));
  const sel = [...controller.selection.selected].sort();
  console.log(
    `focus=${controller.focus.id ?? '-'} selection=[${sel.join(', ')}] ` +
      `version=${controller.tree.version} width=${controller.contentWidth(fixtureGlyphs)}px`,
  );
}

function formatRow(r: Row): string {
  const twisty = r.hasChildren || r.expanded || controller.tree.childrenOf(r.id).length > 0
    ? r.expanded ? 'v' : '>'
    : ' ';
  const focus = r.focused ? '>' : ' ';
  const sel = r.selected ? '[x]' : '[ ]';
  const dis = r.disabled ? ' (disabled)' : '';
  return `${focus} ${twisty} ${'  '.repeat(r.depth)}${sel} ${r.label}${dis}`;
}

// 1. Expand two branches and walk the focus down (skips disabled rows).
controller.expand('alpha');
controller.expand('gamma');
controller.home();
controller.moveDown();
controller.moveDown(); // lands on alpha-three: alpha-two is disabled and skipped
render('expand alpha+gamma, Home, Down x2 (disabled skipped)');

// 2. Shift+Down extends the selection in visible order; disabled excluded.
controller.moveDown(true);
controller.moveDown(true);
render('Shift+Down x2 (range by visible order, disabled excluded)');

// 3. Type-ahead: "ga" jumps to gamma.
controller.typeChar('g');
controller.typeChar('a');
render("type-ahead 'ga'");

// 4. Expand the lazy branch: children arrive asynchronously.
controller.expand('remote');
render('expand remote (load pending)');
await new Promise((r) => setTimeout(r, 0));
render('remote children loaded (selection preserved, expansion of others kept)');

// 5. Collapse the subtree containing the focus: focus falls to the parent.
controller.click('alpha-three-x');
controller.collapse('alpha');
render('focus inside alpha, collapse alpha -> focus on alpha');

// 6. Delete the focused node: deterministic fallback to next enabled row.
controller.removeNode('alpha');
render('remove focused alpha -> fallback to next enabled visible row');

// 7. Filter: matching nodes plus their ancestors, expansion ignored.
controller.setFilter('one');
render("filter 'one'");
controller.setFilter('');

// 8. Keyboard replay: a few raw key events.
controller.home();
controller.handleKey('ArrowDown', { shift: true });
controller.handleKey('End');
controller.handleKey(' ');
render('keys: Home, Shift+Down, End, Space');
