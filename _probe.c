#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    int magic;
    unsigned int length;
    char *data;
} krb5_data;
typedef void *krb5_context;

typedef int (*init_ctx_fn)(krb5_context *);
typedef int (*encode_fn)(krb5_data **, unsigned int, krb5_data *);
typedef int (*decode_fn)(krb5_context, krb5_data, krb5_data ***, const char *,
                         unsigned int *);

#define KV5M_DATA (-1760647422)

static void show_data(const char *label, krb5_data *d) {
    printf("%s[%u]=<", label, d->length);
    for (unsigned int i = 0; i < d->length; i++) putchar(d->data[i]);
    printf(">\n");
}

int main(void) {
    setbuf(stdout, NULL);
    void *h = NULL;
    const char *paths[] = {
        "/System/Library/PrivateFrameworks/Heimdal.framework/Heimdal",
        "/System/Library/PrivateFrameworks/Heimdal.framework/Versions/A/Heimdal",
        "/usr/lib/libkrb5.dylib",
        "/System/Library/Frameworks/Kerberos.framework/Kerberos",
    };
    for (int i = 0; i < 4; i++) {
        h = dlopen(paths[i], RTLD_LAZY | RTLD_LOCAL);
        printf("dlopen(%s)=%p err=%s\n", paths[i], h, dlerror());
        if (h) break;
    }
    if (!h) {
        void *self = dlopen(NULL, RTLD_LAZY);
        printf("dlopen(NULL)=%p err=%s\n", self, dlerror());
        h = self;
    }
    init_ctx_fn init_ctx = (init_ctx_fn)dlsym(h, "krb5_init_context");
    encode_fn encode = (encode_fn)dlsym(h, "krb5_domain_x500_encode");
    decode_fn decode = (decode_fn)dlsym(h, "krb5_domain_x500_decode");
    printf("init=%p encode=%p decode=%p err=%s\n", init_ctx, encode, decode, dlerror());
    if (!init_ctx || !encode || !decode) return 1;

    krb5_context ctx = NULL;
    int rc = init_ctx(&ctx);
    printf("init rc=%d ctx=%p\n", rc, ctx);
    if (rc) return 1;

    const char *sets[][8] = {
        {"A.B.C", "D.B.C", "D.E.C", "D.E.F", "E.F.G"},
        {"ATHENA.MIT.EDU", "CSAIL.MIT.EDU"},
        {"/C=US/O=MIT/OU=ATHENA", "/C=US/O=MIT/OU=CSAIL"},
        {"/C=US/O=MIT", "/C=US/O=MIT/OU=CSAIL"},
        {"/C=US/O=MIT/OU=ATHENA", "/C=US/O=OTHER/OU=CSAIL"},
    };
    unsigned int counts[] = {5, 2, 2, 2, 2};
    for (int s = 0; s < 5; s++) {
        int n = (int)counts[s];
        krb5_data **realms = calloc((size_t)n, sizeof(krb5_data *));
        for (int i = 0; i < n; i++) {
            realms[i] = calloc(1, sizeof(krb5_data));
            realms[i]->magic = KV5M_DATA;
            realms[i]->length = (unsigned int)strlen(sets[s][i]);
            realms[i]->data = (char *)sets[s][i];
        }
        krb5_data trans = {0, 0, NULL};
        rc = encode(realms, (unsigned int)n, &trans);
        printf("set %d encode rc=%d: ", s, rc);
        if (rc == 0) show_data("trans", &trans); else printf("\n");
        if (rc == 0) {
            const char *bases[] = {"CLIENT.ORG", "A.B.C", "/C=US/O=CLIENT", ""};
            for (int b = 0; b < 4; b++) {
                krb5_data **out = NULL;
                unsigned int num = 0;
                int drc = decode(ctx, trans, &out, bases[b], &num);
                printf("  decode base=%s rc=%d num=%u:", bases[b], drc, num);
                for (unsigned int i = 0; i < num; i++) {
                    printf(" <%u:", out[i]->length);
                    for (unsigned int j = 0; j < out[i]->length; j++) putchar(out[i]->data[j]);
                    printf(">");
                }
                printf("\n");
            }
        }
        free(realms);
    }
    return 0;
}
