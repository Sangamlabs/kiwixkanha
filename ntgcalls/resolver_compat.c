#ifdef __linux__
#include <resolv.h>

int __dn_expand(const unsigned char *msg, const unsigned char *eomorig, const unsigned char *comp_dn, char *exp_dn, int length) {
    return dn_expand(msg, eomorig, comp_dn, exp_dn, length);
}

int __res_nquery(res_state statp, const char *dname, int class, int type, unsigned char *answer, int anslen) {
    return res_nquery(statp, dname, class, type, answer, anslen);
}

void __res_nclose(res_state statp) {
    res_nclose(statp);
}

int __res_ninit(res_state statp) {
    return res_ninit(statp);
}
#endif

