#
# !INSTALLED! from the adjagent repo — do not edit in place; edit the source repo and re-install.
# !BODY-SHA256! 75b785d2d6b5ebcc5f3aad04671a3abf66d526b082a49156ef5d52cb2dd14640
#
"""kb_tools — the portable KB metadata toolchain (build, validate, query)."""

# Package-wide single source of the kb_tools version (semver; 1.0.0 marks the
# portable-system stabilization). Every CLI's --version reports this value;
# nothing else hardcodes a version string.
__version__ = "1.0.0"
