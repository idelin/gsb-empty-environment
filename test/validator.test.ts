import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { ValidationError, ValidationErrorCode } from '../src/errors.ts';
import {
  BodyKind,
  MessageType,
  type MessageData,
} from '../src/types.ts';
import { MessageValidator, type ValidatorDeps } from '../src/validator.ts';
import {
  buildMessage,
  cloneMessage,
  generateEd25519Key,
  sha256,
  verifyAnyEd25519,
  type Ed25519Key,
} from './support.ts';

const FID = 1;
const NETWORK = 1;
const NOW = 1_000_000_000_000;
const TEN_MINUTES = 10 * 60 * 1000;

function castData(timestamp: number, text = 'hello farcaster'): MessageData {
  return {
    type: MessageType.CAST_ADD,
    fid: FID,
    timestamp,
    network: NETWORK,
    body: { bodyType: BodyKind.CAST_ADD, text, mentions: [] },
  };
}

function signerAddData(timestamp: number, signer: Uint8Array): MessageData {
  return {
    type: MessageType.SIGNER_ADD,
    fid: FID,
    timestamp,
    network: NETWORK,
    body: { bodyType: BodyKind.SIGNER_ADD, signer, name: 'device' },
  };
}

function signerRemoveData(timestamp: number, signer: Uint8Array): MessageData {
  return {
    type: MessageType.SIGNER_REMOVE,
    fid: FID,
    timestamp,
    network: NETWORK,
    body: { bodyType: BodyKind.SIGNER_REMOVE, signer },
  };
}

interface Harness {
  validator: MessageValidator;
  custody: Ed25519Key;
  signer: Ed25519Key;
}

function createHarness(): Harness {
  const custody = generateEd25519Key();
  const signer = generateEd25519Key();
  const deps: ValidatorDeps = {
    hash: sha256,
    verifySignature: verifyAnyEd25519,
    resolveCustodySigner: (fid) => (fid === FID ? custody.publicKey : undefined),
    clock: () => NOW,
  };
  return { validator: new MessageValidator(deps), custody, signer };
}

function addSigner(harness: Harness, timestamp = 1000): void {
  harness.validator.validate(
    buildMessage(signerAddData(timestamp, harness.signer.publicKey), harness.custody),
  );
}

function assertRejectsWithCode(fn: () => unknown, code: ValidationErrorCode): void {
  assert.throws(
    fn,
    (error: unknown) => error instanceof ValidationError && error.code === code,
  );
}

describe('MessageValidator 正常路径', () => {
  test('SignerAdd 后签名者活跃，普通消息可入库', () => {
    const harness = createHarness();
    addSigner(harness);

    const result = harness.validator.validate(
      buildMessage(castData(2000), harness.signer),
    );
    assert.equal(result.accepted, true);
    assert.deepEqual(result.evictedHashes, []);
    assert.equal(harness.validator.isActive(FID, harness.signer.publicKey), true);
    assert.equal(harness.validator.getStoredMessages(FID).length, 2);
  });

  test('未授权签名者的消息被拒绝', () => {
    const { validator } = createHarness();
    const stranger = generateEd25519Key();

    assertRejectsWithCode(
      () => validator.validate(buildMessage(castData(2000), stranger)),
      ValidationErrorCode.SIGNER_NOT_ACTIVE,
    );
  });
});

describe('类型与正文一致性', () => {
  test('CAST_ADD 携带非 cast 正文时拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const message = buildMessage(castData(2000), harness.signer);
    message.data.body = {
      bodyType: BodyKind.REACTION_ADD,
      reactionType: 1,
      targetCastHash: new Uint8Array([1, 2, 3]),
    };

    assertRejectsWithCode(
      () => harness.validator.validate(message),
      ValidationErrorCode.TYPE_BODY_MISMATCH,
    );
  });

  test('正文种类与类型错配、即使重新编码自洽也拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const data = castData(2000);
    data.body = {
      bodyType: BodyKind.CAST_REMOVE,
      targetHash: new Uint8Array([9, 9]),
    };

    assertRejectsWithCode(
      () => harness.validator.validate(buildMessage(data, harness.signer)),
      ValidationErrorCode.TYPE_BODY_MISMATCH,
    );
  });
});

describe('哈希与 data_bytes 绑定', () => {
  test('hash 不等于对 data_bytes 的计算结果时拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const message = buildMessage(castData(2000), harness.signer, {
      hash: new Uint8Array(32).fill(7),
    });

    assertRejectsWithCode(
      () => harness.validator.validate(message),
      ValidationErrorCode.HASH_MISMATCH,
    );
  });

  test('结构化字段与 data_bytes 解码结果不一致时拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const tampered = cloneMessage(
      buildMessage(castData(2000), harness.signer),
    );
    tampered.data.body = {
      bodyType: BodyKind.CAST_ADD,
      text: 'tampered text',
      mentions: [],
    };

    assertRejectsWithCode(
      () => harness.validator.validate(tampered),
      ValidationErrorCode.DATA_BYTES_MISMATCH,
    );
  });

  test('非规范编码但能解码成相同对象时，因重编码不一致拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const data = castData(2000);
    const nonCanonicalBytes = new TextEncoder().encode(
      JSON.stringify(data).replaceAll('":', '" :'),
    );
    const message = buildMessage(data, harness.signer, {
      dataBytes: nonCanonicalBytes,
    });

    assertRejectsWithCode(
      () => harness.validator.validate(message),
      ValidationErrorCode.DATA_BYTES_MISMATCH,
    );
  });
});

describe('未来时间戳', () => {
  test('超过当前时间 10 分钟拒绝，恰好 10 分钟接受', () => {
    const harness = createHarness();
    addSigner(harness);

    assertRejectsWithCode(
      () =>
        harness.validator.validate(
          buildMessage(castData(NOW + TEN_MINUTES + 1), harness.signer),
        ),
      ValidationErrorCode.FUTURE_TIMESTAMP,
    );

    const boundary = harness.validator.validate(
      buildMessage(castData(NOW + TEN_MINUTES), harness.signer),
    );
    assert.equal(boundary.accepted, true);
  });
});

describe('签名校验', () => {
  test('签名与消息哈希不对应时拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const message = buildMessage(castData(2000), harness.signer);
    message.signature[0] = message.signature[0] ^ 0xff;

    assertRejectsWithCode(
      () => harness.validator.validate(message),
      ValidationErrorCode.INVALID_SIGNATURE,
    );
  });

  test('SignerRemove 由非托管密钥签发时拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const message = buildMessage(
      signerRemoveData(3000, harness.signer.publicKey),
      harness.signer,
    );

    assertRejectsWithCode(
      () => harness.validator.validate(message),
      ValidationErrorCode.UNKNOWN_CUSTODY,
    );
  });
});

describe('签名者移除与重放', () => {
  test('SignerRemove 逐出该签名者历史消息，之后新消息拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const first = harness.validator.validate(
      buildMessage(castData(2000), harness.signer),
    );
    const second = harness.validator.validate(
      buildMessage(castData(2100, 'second cast'), harness.signer),
    );
    assert.equal(harness.validator.getStoredMessages(FID).length, 3);

    const removeResult = harness.validator.validate(
      buildMessage(signerRemoveData(3000, harness.signer.publicKey), harness.custody),
    );
    const evicted = removeResult.evictedHashes.map((digest) =>
      Buffer.from(digest).toString('hex'),
    );
    assert.deepEqual(
      evicted.sort(),
      [first.message.hash, second.message.hash]
        .map((digest) => Buffer.from(digest).toString('hex'))
        .sort(),
    );
    assert.equal(harness.validator.isActive(FID, harness.signer.publicKey), false);
    // 两条应用消息被逐出，SignerAdd / SignerRemove 管理消息保留。
    assert.equal(harness.validator.getStoredMessages(FID).length, 2);

    assertRejectsWithCode(
      () =>
        harness.validator.validate(
          buildMessage(castData(4000, 'after removal'), harness.signer),
        ),
      ValidationErrorCode.SIGNER_NOT_ACTIVE,
    );
  });

  test('移除前旧消息的重放同样被拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const cast = harness.validator.validate(
      buildMessage(castData(2000), harness.signer),
    ).message;
    harness.validator.validate(
      buildMessage(signerRemoveData(3000, harness.signer.publicKey), harness.custody),
    );

    assertRejectsWithCode(
      () => harness.validator.validate(cloneMessage(cast)),
      ValidationErrorCode.SIGNER_NOT_ACTIVE,
    );
  });

  test('移除从未生效或尚未生效的签名者时拒绝', () => {
    const harness = createHarness();
    const neverAdded = generateEd25519Key();
    assertRejectsWithCode(
      () =>
        harness.validator.validate(
          buildMessage(signerRemoveData(3000, neverAdded.publicKey), harness.custody),
        ),
      ValidationErrorCode.SIGNER_NOT_ACTIVE,
    );

    addSigner(harness, 5000);
    assertRejectsWithCode(
      () =>
        harness.validator.validate(
          buildMessage(signerRemoveData(4000, harness.signer.publicKey), harness.custody),
        ),
      ValidationErrorCode.SIGNER_NOT_ACTIVE,
    );
  });
});

describe('SignerAdd / SignerRemove 时间戳平局', () => {
  test('同时间戳时移除胜出：平局的 SignerAdd 被压制，签名者不活跃', () => {
    const harness = createHarness();
    addSigner(harness, 5000);
    harness.validator.validate(buildMessage(castData(5100), harness.signer));

    harness.validator.validate(
      buildMessage(signerRemoveData(6000, harness.signer.publicKey), harness.custody),
    );
    assert.equal(harness.validator.isActive(FID, harness.signer.publicKey), false);

    assertRejectsWithCode(
      () =>
        harness.validator.validate(
          buildMessage(signerAddData(6000, harness.signer.publicKey), harness.custody),
        ),
      ValidationErrorCode.SIGNER_ADD_SUPPRESSED,
    );
    assert.equal(harness.validator.isActive(FID, harness.signer.publicKey), false);

    assertRejectsWithCode(
      () => harness.validator.validate(buildMessage(castData(6100), harness.signer)),
      ValidationErrorCode.SIGNER_NOT_ACTIVE,
    );
  });

  test('先收到同时间戳 Add 再收到 Remove 时，Remove 仍然胜出并逐出消息', () => {
    const harness = createHarness();
    addSigner(harness, 4000);
    const cast = harness.validator.validate(
      buildMessage(castData(5000), harness.signer),
    );

    harness.validator.validate(
      buildMessage(signerAddData(6000, harness.signer.publicKey), harness.custody),
    );
    const removeResult = harness.validator.validate(
      buildMessage(signerRemoveData(6000, harness.signer.publicKey), harness.custody),
    );

    assert.equal(harness.validator.isActive(FID, harness.signer.publicKey), false);
    assert.equal(removeResult.evictedHashes.length, 1);
    assert.deepEqual(removeResult.evictedHashes[0], cast.message.hash);
  });

  test('晚于移除时间戳的 SignerAdd 可以重新激活签名者', () => {
    const harness = createHarness();
    addSigner(harness, 4000);
    harness.validator.validate(
      buildMessage(signerRemoveData(5000, harness.signer.publicKey), harness.custody),
    );
    harness.validator.validate(
      buildMessage(signerAddData(6000, harness.signer.publicKey), harness.custody),
    );

    assert.equal(harness.validator.isActive(FID, harness.signer.publicKey), true);
    const result = harness.validator.validate(
      buildMessage(castData(7000), harness.signer),
    );
    assert.equal(result.accepted, true);
  });
});

describe('重复消息', () => {
  test('相同 hash 的消息二次提交被拒绝', () => {
    const harness = createHarness();
    addSigner(harness);
    const message = buildMessage(castData(2000), harness.signer);
    harness.validator.validate(message);

    assertRejectsWithCode(
      () => harness.validator.validate(cloneMessage(message)),
      ValidationErrorCode.DUPLICATE_MESSAGE,
    );
  });
});
