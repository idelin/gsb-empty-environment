import {
  SharedWorkerProxy,
  createBrowserWorkerFactory,
  type ConnectRequest,
} from '../src/index.ts';

const proxy = new SharedWorkerProxy(createBrowserWorkerFactory());

const request: ConnectRequest = {
  credentials: {
    tenantId: 'tenant-a',
    secret: 'replace-with-a-real-secret-a',
  },
  scriptUrl: new URL('tenant-worker.js', self.location.href).href,
};

const channel = await proxy.connect(request);

channel.port.postMessage({ id: 1, kind: 'echo', payload: { hello: 'tenant-a' } });
channel.port.addEventListener('message', (event) => {
  console.log('tenant channel received', event.data);
});
channel.port.start();

console.log('connected via derived worker name', channel.workerName);
