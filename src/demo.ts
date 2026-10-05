/** 演示：一个同时包含整数区间与枚举属性的有序规则集 */
import { Analyzer, formatReport } from './analyzer.ts';
import type { Domain } from './model.ts';

const domain: Domain = {
  attrs: [
    { kind: 'int', name: 'port', min: 0, max: 65535 },
    { kind: 'enum', name: 'proto', values: ['tcp', 'udp', 'icmp'] },
  ],
};

const analyzer = new Analyzer(domain);
analyzer.addRule({ id: 'R1', action: 'allow', conds: [{ kind: 'int', attr: 'port', lo: 0, hi: 1024, hiOpen: true }, { kind: 'enum', attr: 'proto', values: ['tcp'] }] });
analyzer.addRule({ id: 'R2', action: 'deny', conds: [{ kind: 'int', attr: 'port', lo: 512, hi: 2048 }, { kind: 'enum', attr: 'proto', values: ['tcp', 'udp'] }] });
analyzer.addRule({ id: 'R3', action: 'log', conds: [{ kind: 'int', attr: 'port', lo: 80, hi: 80 }] });
analyzer.addRule({ id: 'R4', action: 'deny', conds: [{ kind: 'int', attr: 'port', lo: 6000, hi: 100 }] }); // 空区间
console.log(formatReport(analyzer.analyze()));
