import './plugin-host.test';
import { run } from './runner';

declare const process: {
  exitCode: number;
};

run().then((failures) => {
  process.exitCode = failures === 0 ? 0 : 1;
});
